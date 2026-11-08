package grpcapp

import (
	"context"
	"errors"
	"log/slog"
	"net"

	"google.golang.org/grpc"

	"cloud/internal/config"
	s3grpc "cloud/internal/grpc/s3"
)

type App struct {
	log      *slog.Logger
	server   *grpc.Server
	listener net.Listener
}

func New(log *slog.Logger, service s3grpc.S3, cfg config.Config) (*App, error) {
	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return nil, err
	}
	handler := s3grpc.New(cfg, service, log.With("component", "grpc.s3"))
	server := grpc.NewServer(grpc.UnaryInterceptor(handler.UnaryInterceptor), grpc.StreamInterceptor(handler.StreamInterceptor), grpc.MaxRecvMsgSize(4<<20), grpc.MaxSendMsgSize(4<<20))
	handler.RegisterService(server)
	return &App{log: log.With("component", "app.grpc"), server: server, listener: listener}, nil
}
func (a *App) Run() error {
	a.log.Info("gRPC server started", "addr", a.listener.Addr().String())
	err := a.server.Serve(a.listener)
	if errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
func (a *App) Target() string {
	host, port, _ := net.SplitHostPort(a.listener.Addr().String())
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
func (a *App) Stop(ctx context.Context) {
	stopped := make(chan struct{})
	go func() { a.server.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-ctx.Done():
		a.server.Stop()
	}
	_ = a.listener.Close()
}
