package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	grpcapp "cloud/internal/app/grpc"
	httpapp "cloud/internal/app/http"
	"cloud/internal/config"
	"cloud/internal/monitoring"
	"cloud/internal/repo/mongo"
	"cloud/internal/repo/s3"
	s3service "cloud/internal/service/s3"
	"cloud/pkg/auth"
)

type App struct {
	GRPCServer *grpcapp.App
	HTTPServer *httpapp.App
	ctx        context.Context
	cancel     context.CancelFunc
	database   *mongo.DB
	store      *s3.Store
	monitor    *monitoring.Monitor
	log        *slog.Logger
	stop       sync.Once
}

func New(parent context.Context, log *slog.Logger, cfg config.Config) (*App, error) {
	ctx, cancel := context.WithCancel(parent)
	var database *mongo.DB
	if err := retry(ctx, log, "mongodb", func() (err error) { database, err = mongo.Connect(ctx, cfg.MongoURI, cfg.MongoDB); return err }); err != nil {
		cancel()
		return nil, err
	}
	store := s3.New(cfg.S3Endpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket)
	business := s3service.NewService(s3service.Config{UserQuotaBytes: cfg.UserQuotaBytes, MaxUploadBytes: cfg.MaxUploadBytes}, database, store, auth.New(cfg.JWTSecret, cfg.JWTTTL), log)
	monitor := monitoring.New(database, store, cfg.DependencyInterval, log)
	rpc, err := grpcapp.New(log, business, cfg)
	if err != nil {
		cancel()
		_ = database.Close(context.Background())
		return nil, err
	}
	http, err := httpapp.New(ctx, log, cfg.HTTPAddr, rpc.Target(), httpapp.ReadyHandler(database, store))
	if err != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		rpc.Stop(stopCtx)
		cancel()
		_ = database.Close(stopCtx)
		return nil, err
	}
	return &App{GRPCServer: rpc, HTTPServer: http, ctx: ctx, cancel: cancel, database: database, store: store, monitor: monitor, log: log.With("component", "app")}, nil
}

func (a *App) Run() error {
	go a.ensureBucket()
	go a.monitor.Run(a.ctx)
	failures := make(chan error, 2)
	go func() { failures <- a.GRPCServer.Run() }()
	go func() { failures <- a.HTTPServer.Run() }()
	select {
	case <-a.ctx.Done():
		return nil
	case err := <-failures:
		return err
	}
}

func (a *App) Stop() {
	a.stop.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// Drain gateway requests before closing their gRPC upstream and dependencies.
		a.HTTPServer.Stop(ctx)
		a.GRPCServer.Stop(ctx)
		a.cancel()
		if err := a.database.Close(ctx); err != nil {
			a.log.Warn("database shutdown", "err", err)
		}
		a.log.Info("application stopped")
	})
}

func (a *App) ensureBucket() {
	for {
		if err := a.store.EnsureBucket(a.ctx); err == nil {
			a.log.Info("bucket ready", "bucket", a.store.Bucket())
			return
		} else {
			a.log.Warn("waiting for object storage", "err", err)
		}
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func retry(ctx context.Context, log *slog.Logger, name string, fn func() error) error {
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if attempt >= 30 {
			return err
		}
		log.Warn("waiting for dependency", "dependency", name, "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
