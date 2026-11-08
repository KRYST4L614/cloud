package httpapp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type App struct {
	log        *slog.Logger
	server     *http.Server
	connection *grpc.ClientConn
}

func New(ctx context.Context, log *slog.Logger, address, target string, ready http.HandlerFunc) (*App, error) {
	connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	handler, err := newHandler(ctx, connection, ready, log.With("component", "http.gateway"))
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &App{log: log.With("component", "app.http"), server: &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second}, connection: connection}, nil
}
func (a *App) Run() error {
	a.log.Info("HTTP gateway started", "addr", a.server.Addr)
	err := a.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func (a *App) Stop(ctx context.Context) {
	if err := a.server.Shutdown(ctx); err != nil {
		a.log.Warn("HTTP shutdown", "err", err)
		_ = a.server.Close()
	}
	_ = a.connection.Close()
}
