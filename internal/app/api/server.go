package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.mongodb.org/mongo-driver/v2/bson"

	apispec "cloud/api"
	"cloud/internal/auth"
	"cloud/internal/config"
	"cloud/internal/metrics"
	"cloud/internal/repository/mongo"
	"cloud/internal/storage/s3"
)

type Server struct {
	cfg   config.Config
	db    *mongo.DB
	store *s3.Store
	auth  *auth.Manager
	log   *slog.Logger
}

func New(cfg config.Config, d *mongo.DB, s *s3.Store, a *auth.Manager, log *slog.Logger) *Server {
	return &Server{cfg: cfg, db: d, store: s, auth: a, log: log}
}

type route struct {
	pattern string
	handler http.Handler
}

func (s *Server) routes() []route {
	return []route{
		{"GET /healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })},
		{"GET /readyz", http.HandlerFunc(s.ready)},
		{"GET /metrics", promhttp.Handler()},
		{"GET /api/openapi.yaml", http.HandlerFunc(serveOpenAPI)},

		{"POST /api/auth/register", http.HandlerFunc(s.register)},
		{"POST /api/auth/login", http.HandlerFunc(s.login)},
		{"GET /api/me", s.authed(s.me)},

		{"GET /api/files", s.authed(s.listFiles)},
		{"POST /api/files", s.authed(s.uploadFile)},
		{"GET /api/files/{id}/download", s.authed(s.downloadFile)},
		{"PATCH /api/files/{id}", s.authed(s.renameFile)},
		{"DELETE /api/files/{id}", s.authed(s.deleteFile)},
		{"POST /api/files/{id}/share", s.authed(s.shareFile)},
		{"DELETE /api/files/{id}/share", s.authed(s.unshareFile)},
		{"GET /api/public/{token}", http.HandlerFunc(s.publicDownload)},

		{"GET /api/alerts", s.authed(s.listAlerts)},
		{"POST /api/alerts/webhook", http.HandlerFunc(s.alertWebhook)},

		{"POST /internal/s3-events", http.HandlerFunc(s.storageEvents)},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, r := range s.routes() {
		mux.Handle(r.pattern, r.handler)
	}
	return s.instrument(mux)
}

func serveOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.Write(apispec.Spec)
}

func (s *Server) MonitorDependencies(ctx context.Context) {
	t := time.NewTicker(s.cfg.DependencyInterval)
	defer t.Stop()
	for {
		s.checkDependencies(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) checkDependencies(ctx context.Context) {
	check := func(name string, ping func(context.Context) error) bool {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		start := time.Now()
		err := ping(ctx)
		metrics.DependencyLatency.WithLabelValues(name).Set(time.Since(start).Seconds())
		if err != nil {
			s.log.Warn("dependency check failed", "dependency", name, "err", err)
			metrics.DependencyUp.WithLabelValues(name).Set(0)
			return false
		}
		metrics.DependencyUp.WithLabelValues(name).Set(1)
		return true
	}
	check("s3", s.store.Ping)
	if !check("mongodb", s.db.Ping) {
		return
	}
	st, err := s.db.Stats(ctx)
	if err != nil {
		s.log.Warn("collect stats", "err", err)
		return
	}
	metrics.Users.Set(float64(st.Users))
	metrics.Files.Set(float64(st.Files))
	metrics.UsedBytes.Set(float64(st.UsedBytes))
	metrics.QuotaBytes.Set(float64(st.QuotaBytes))
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	status := map[string]string{"s3": "ok", "mongodb": "ok"}
	code := http.StatusOK
	if err := s.store.Ping(ctx); err != nil {
		status["s3"], code = err.Error(), http.StatusServiceUnavailable
	}
	if err := s.db.Ping(ctx); err != nil {
		status["mongodb"], code = err.Error(), http.StatusServiceUnavailable
	}
	writeJSON(w, code, status)
}

type ctxKey struct{}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		} else if i := strings.IndexByte(route, ' '); i >= 0 {
			route = route[i+1:]
		}
		if route == "/metrics" {
			return
		}
		elapsed := time.Since(start)
		metrics.HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		metrics.HTTPDuration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
		if route != "/healthz" {
			s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", elapsed)
		}
	})
}

func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			token = r.URL.Query().Get("access_token")
		}
		sub, err := s.auth.Verify(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		uid, err := bson.ObjectIDFromHex(sub)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, uid)))
	}
}

func userID(r *http.Request) bson.ObjectID { return r.Context().Value(ctxKey{}).(bson.ObjectID) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.log.Error("internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) dbError(w http.ResponseWriter, err error) {
	if errors.Is(err, mongo.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.internalError(w, err)
}
