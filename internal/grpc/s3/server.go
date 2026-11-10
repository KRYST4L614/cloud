package s3grpc

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	cloudv1 "cloud/gen/go/cloud/v1"
	"cloud/internal/config"
	"cloud/internal/domain"
	"cloud/internal/metrics"
)

type S3 interface {
	Authenticate(string) (string, error)
	Register(context.Context, domain.Credentials) (*domain.Session, error)
	Login(context.Context, domain.Credentials) (*domain.Session, error)
	Profile(context.Context, string) (*domain.User, error)
	ListFiles(context.Context, string, string) ([]domain.File, error)
	Upload(context.Context, string, domain.Upload) (*domain.File, error)
	Download(context.Context, string, string) (*domain.Download, error)
	PublicDownload(context.Context, string) (*domain.Download, error)
	Rename(context.Context, string, string, string) error
	Delete(context.Context, string, string) error
	Share(context.Context, string, string) (*domain.ShareLink, error)
	Unshare(context.Context, string, string) error
	ListAlerts(context.Context, string) ([]domain.Alert, error)
	ReceiveAlerts(context.Context, []domain.Alert) error
	ReceiveStorageEvents(context.Context, domain.StorageNotification) error
}

type Handler struct {
	cloudv1.UnimplementedCloudStorageServer
	cfg     config.Config
	service S3
	log     *slog.Logger
}

func New(cfg config.Config, service S3, log *slog.Logger) *Handler {
	return &Handler{cfg: cfg, service: service, log: log}
}
func (h *Handler) RegisterService(server *grpc.Server) { cloudv1.RegisterCloudStorageServer(server, h) }

type ctxKey struct{}

func userID(ctx context.Context) string { return ctx.Value(ctxKey{}).(string) }

func (s *Handler) authorize(ctx context.Context, method string) (context.Context, error) {
	switch method {
	case cloudv1.CloudStorage_Register_FullMethodName, cloudv1.CloudStorage_Login_FullMethodName, cloudv1.CloudStorage_PublicDownload_FullMethodName:
		return ctx, nil
	}
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	token := ""
	if len(values) > 0 {
		token = strings.TrimPrefix(values[0], "Bearer ")
	}
	switch method {
	case cloudv1.CloudStorage_ReceiveAlerts_FullMethodName, cloudv1.CloudStorage_ReceiveStorageEvents_FullMethodName:
		secret := s.cfg.AlertWebhookToken
		if method == cloudv1.CloudStorage_ReceiveStorageEvents_FullMethodName {
			secret = s.cfg.S3EventToken
		}
		if secret == "" || subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
			return nil, status.Error(codes.Unauthenticated, "Unauthorized")
		}
		return ctx, nil
	}
	id, err := s.service.Authenticate(token)
	if err != nil {
		return nil, s.failure(err)
	}
	return context.WithValue(ctx, ctxKey{}, id), nil
}

func (s *Handler) UnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	start := time.Now()
	ctx, err := s.authorize(ctx, info.FullMethod)
	var response any
	if err == nil {
		response, err = handler(ctx, req)
	}
	s.observeRPC(info.FullMethod, start, err)
	return response, err
}

type authenticatedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authenticatedStream) Context() context.Context { return s.ctx }

func (s *Handler) StreamInterceptor(service any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()
	ctx, err := s.authorize(stream.Context(), info.FullMethod)
	if err == nil {
		err = handler(service, &authenticatedStream{ServerStream: stream, ctx: ctx})
	}
	s.observeRPC(info.FullMethod, start, err)
	return err
}

func (s *Handler) observeRPC(method string, start time.Time, err error) {
	metrics.GRPCRequests.WithLabelValues(method, status.Code(err).String()).Inc()
	metrics.GRPCDuration.WithLabelValues(method).Observe(time.Since(start).Seconds())
	s.log.Info("grpc request", "method", method, "code", status.Code(err).String(), "duration", time.Since(start))
}

func (s *Handler) failure(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	message := err.Error()
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		message = domainError.Message
	}
	for _, item := range []struct {
		kind error
		code codes.Code
	}{
		{domain.ErrNotFound, codes.NotFound}, {domain.ErrDuplicate, codes.AlreadyExists}, {domain.ErrQuotaExceeded, codes.ResourceExhausted},
		{domain.ErrInvalidArgument, codes.InvalidArgument}, {domain.ErrUnauthenticated, codes.Unauthenticated}, {domain.ErrUnavailable, codes.Unavailable},
	} {
		if errors.Is(err, item.kind) {
			return status.Error(item.code, message)
		}
	}
	s.log.Error("internal error", "err", err)
	return status.Error(codes.Internal, "Internal error")
}

func httpStatus(ctx context.Context, code string) {
	_ = grpc.SetHeader(ctx, metadata.Pairs("x-http-code", code))
}
