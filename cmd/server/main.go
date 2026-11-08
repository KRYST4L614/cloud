package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud/internal/app/api"
	"cloud/internal/auth"
	"cloud/internal/config"
	"cloud/internal/repository/mongo"
	"cloud/internal/storage/s3"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var database *mongo.DB
	if err := retry(ctx, log, "mongodb", func() (err error) {
		database, err = mongo.Connect(ctx, cfg.MongoURI, cfg.MongoDB)
		return err
	}); err != nil {
		return err
	}
	defer database.Close(context.Background())

	store := s3.New(cfg.S3Endpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket)

	go ensureBucket(ctx, log, store)

	srv := api.New(cfg, database, store, auth.New(cfg.JWTSecret, cfg.JWTTTL), log)
	go srv.MonitorDependencies(ctx)

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdownCtx)
	}()

	log.Info("listening", "addr", cfg.HTTPAddr)
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func ensureBucket(ctx context.Context, log *slog.Logger, store *s3.Store) {
	for {
		err := store.EnsureBucket(ctx)
		if err == nil {
			log.Info("bucket ready", "bucket", store.Bucket())
			return
		}
		log.Warn("waiting for object storage", "err", err)
		select {
		case <-ctx.Done():
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
