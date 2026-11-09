package domain

import (
	"errors"
	"io"
	"time"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrDuplicate       = errors.New("user already exists")
	ErrQuotaExceeded   = errors.New("storage quota exceeded")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrUnauthenticated = errors.New("unauthorized")
	ErrUnavailable     = errors.New("service unavailable")
)

type Error struct {
	Kind    error
	Message string
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Kind }

type User struct {
	ID           string
	Email        string
	PasswordHash string
	QuotaBytes   int64
	UsedBytes    int64
	CreatedAt    time.Time
}

type Credentials struct{ Email, Password string }
type Session struct {
	Token     string
	ExpiresAt time.Time
	User      *User
}

type File struct {
	ID, OwnerID                        string
	Name                               string
	Size                               int64
	ContentType, ObjectKey, ShareToken string
	CreatedAt                          time.Time
}

type Upload struct {
	Name string
	Body io.Reader
}
type Download struct {
	File *File
	Body io.ReadCloser
}
type ShareLink struct{ Token, URL string }

type Alert struct {
	ID, Fingerprint, Name, Status, Severity, Summary, Description string
	Labels                                                        map[string]string
	StartsAt, EndsAt, UpdatedAt                                   time.Time
}

type StorageEvent struct{ Name, Bucket, Key string }
type StorageNotification struct {
	Event   string
	Records []StorageEvent
}
type Stats struct{ Users, Files, UsedBytes, QuotaBytes int64 }
