package httpapp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type HealthChecker interface {
	Ping(context.Context) error
}

func ReadyHandler(database, store HealthChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		status := map[string]string{"s3": "ok", "mongodb": "ok"}
		code := http.StatusOK
		if err := store.Ping(ctx); err != nil {
			status["s3"], code = err.Error(), http.StatusServiceUnavailable
		}
		if err := database.Ping(ctx); err != nil {
			status["mongodb"], code = err.Error(), http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(status)
	}
}
