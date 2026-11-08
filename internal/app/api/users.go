package api

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"cloud/internal/auth"
	"cloud/internal/metrics"
	"cloud/internal/repository/mongo"
)

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResponse struct {
	Token     string      `json:"token"`
	ExpiresAt time.Time   `json:"expires_at"`
	User      *mongo.User `json:"user"`
}

func (c *credentials) normalize() {
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := decodeJSON(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c.normalize()
	if _, err := mail.ParseAddress(c.Email); err != nil {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if len(c.Password) < 8 || len(c.Password) > 72 {
		writeError(w, http.StatusBadRequest, "password must be 8-72 characters long")
		return
	}
	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		s.internalError(w, err)
		return
	}
	u := &mongo.User{Email: c.Email, PasswordHash: hash, QuotaBytes: s.cfg.UserQuotaBytes}
	if err := s.db.CreateUser(r.Context(), u); err != nil {
		if errors.Is(err, mongo.ErrDuplicate) {
			metrics.AuthEvents.WithLabelValues("register", "conflict").Inc()
			writeError(w, http.StatusConflict, "user already exists")
			return
		}
		s.internalError(w, err)
		return
	}
	metrics.AuthEvents.WithLabelValues("register", "ok").Inc()
	s.respondToken(w, http.StatusCreated, u)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := decodeJSON(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c.normalize()
	u, err := s.db.UserByEmail(r.Context(), c.Email)
	if err != nil && !errors.Is(err, mongo.ErrNotFound) {
		s.internalError(w, err)
		return
	}
	if u == nil || !auth.CheckPassword(u.PasswordHash, c.Password) {
		metrics.AuthEvents.WithLabelValues("login", "fail").Inc()
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	metrics.AuthEvents.WithLabelValues("login", "ok").Inc()
	s.respondToken(w, http.StatusOK, u)
}

func (s *Server) respondToken(w http.ResponseWriter, code int, u *mongo.User) {
	tok, exp, err := s.auth.Issue(u.ID.Hex())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, code, tokenResponse{Token: tok, ExpiresAt: exp, User: u})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, err := s.db.UserByID(r.Context(), userID(r))
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}
