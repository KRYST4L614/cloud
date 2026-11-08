package s3

import (
	"context"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"

	"cloud/internal/domain"
	"cloud/internal/metrics"
	"cloud/pkg/auth"
)

func (s *Service) Register(ctx context.Context, credentials domain.Credentials) (*domain.Session, error) {
	email := strings.ToLower(strings.TrimSpace(credentials.Email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return nil, invalid("Invalid email")
	}
	if len(credentials.Password) < 8 || len(credentials.Password) > 72 {
		return nil, invalid("Password must be 8-72 bytes long")
	}
	hash, err := auth.HashPassword(credentials.Password)
	if err != nil {
		return nil, err
	}
	user := &domain.User{Email: email, PasswordHash: hash, QuotaBytes: s.cfg.UserQuotaBytes}
	if err = s.db.CreateUser(ctx, user); err != nil {
		metrics.AuthEvents.WithLabelValues("register", "error").Inc()
		return nil, err
	}
	metrics.AuthEvents.WithLabelValues("register", "ok").Inc()
	return s.session(user)
}

func (s *Service) Login(ctx context.Context, credentials domain.Credentials) (*domain.Session, error) {
	user, err := s.db.UserByEmail(ctx, strings.ToLower(strings.TrimSpace(credentials.Email)))
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if user == nil || !auth.CheckPassword(user.PasswordHash, credentials.Password) {
		metrics.AuthEvents.WithLabelValues("login", "fail").Inc()
		return nil, &domain.Error{Kind: domain.ErrUnauthenticated, Message: "Invalid email or password"}
	}
	metrics.AuthEvents.WithLabelValues("login", "ok").Inc()
	return s.session(user)
}

func (s *Service) Authenticate(token string) (string, error) {
	id, err := s.auth.Verify(token)
	if err != nil || !validID(id) {
		return "", domain.ErrUnauthenticated
	}
	return id, nil
}

func (s *Service) Profile(ctx context.Context, owner string) (*domain.User, error) {
	return s.db.UserByID(ctx, owner)
}
func (s *Service) session(user *domain.User) (*domain.Session, error) {
	token, expires, err := s.auth.Issue(user.ID)
	if err != nil {
		return nil, err
	}
	return &domain.Session{Token: token, ExpiresAt: expires, User: user}, nil
}

func validID(id string) bool {
	if len(id) != 24 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
