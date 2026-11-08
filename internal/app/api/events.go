package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"cloud/internal/metrics"
	"cloud/internal/repository/mongo"
)

type s3EventNotification struct {
	Event   string `json:"Event"`
	Records []struct {
		EventName string `json:"eventName"`
		S3        struct {
			Bucket struct {
				Name string `json:"name"`
			} `json:"bucket"`
			Object struct {
				Key string `json:"key"`
			} `json:"object"`
		} `json:"s3"`
	} `json:"Records"`
}

func (s *Server) storageEvents(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.S3EventToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var n s3EventNotification
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&n); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if n.Event != "" {
		metrics.StorageEvents.WithLabelValues(n.Event).Inc()
	}
	for _, rec := range n.Records {
		metrics.StorageEvents.WithLabelValues(rec.EventName).Inc()
		if rec.S3.Bucket.Name != s.store.Bucket() || !strings.HasPrefix(rec.EventName, "s3:ObjectRemoved:") {
			continue
		}

		key, err := url.QueryUnescape(rec.S3.Object.Key)
		if err != nil {
			key = rec.S3.Object.Key
		}
		if err := s.forgetObject(r.Context(), key, "event"); err != nil {
			s.internalError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) forgetObject(ctx context.Context, key, source string) error {
	ctx = context.WithoutCancel(ctx)
	f, err := s.db.DeleteFileByKey(ctx, key)
	if errors.Is(err, mongo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.db.ReleaseSpace(ctx, f.OwnerID, f.Size); err != nil {
		return err
	}
	metrics.SyncRemovedFiles.WithLabelValues(source).Inc()
	s.log.Info("file removed outside the app", "source", source, "key", key, "file", f.ID.Hex(), "name", f.Name)
	return nil
}
