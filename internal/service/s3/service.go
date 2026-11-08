package s3

import (
	"context"
	"io"
	"log/slog"

	"cloud/internal/domain"
	"cloud/pkg/auth"
)

type Repository interface {
	CreateUser(context.Context, *domain.User) error
	UserByEmail(context.Context, string) (*domain.User, error)
	UserByID(context.Context, string) (*domain.User, error)
	ReserveSpace(context.Context, string, int64) error
	ReleaseSpace(context.Context, string, int64) error
	CreateFile(context.Context, *domain.File) error
	ListFiles(context.Context, string, string) ([]domain.File, error)
	FileByID(context.Context, string, string) (*domain.File, error)
	FileByShareToken(context.Context, string) (*domain.File, error)
	RenameFile(context.Context, string, string, string) error
	SetShareToken(context.Context, string, string, string) error
	DeleteFile(context.Context, string, string) (*domain.File, error)
	DeleteFileByKey(context.Context, string) (*domain.File, error)
	UpsertAlert(context.Context, *domain.Alert) error
	ListAlerts(context.Context, int64) ([]domain.Alert, error)
}

type ObjectStore interface {
	Bucket() string
	Put(context.Context, string, string, io.Reader) error
	Get(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

type Config struct{ UserQuotaBytes, MaxUploadBytes int64 }

type Service struct {
	cfg   Config
	db    Repository
	store ObjectStore
	auth  *auth.Manager
	log   *slog.Logger
}

func NewService(cfg Config, db Repository, store ObjectStore, auth *auth.Manager, log *slog.Logger) *Service {
	return &Service{cfg: cfg, db: db, store: store, auth: auth, log: log}
}

func invalid(message string) error {
	return &domain.Error{Kind: domain.ErrInvalidArgument, Message: message}
}
