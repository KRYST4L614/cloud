package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"cloud/internal/metrics"
	"cloud/internal/repository/mongo"
)

type grafanaWebhook struct {
	Status string `json:"status"`
	Alerts []struct {
		Status      string            `json:"status"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
		StartsAt    time.Time         `json:"startsAt"`
		EndsAt      time.Time         `json:"endsAt"`
		Fingerprint string            `json:"fingerprint"`
	} `json:"alerts"`
}

func (s *Server) alertWebhook(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AlertWebhookToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var payload grafanaWebhook
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	for _, a := range payload.Alerts {
		alert := &mongo.Alert{
			Fingerprint: a.Fingerprint,
			Name:        a.Labels["alertname"],
			Status:      a.Status,
			Severity:    a.Labels["severity"],
			Summary:     a.Annotations["summary"],
			Description: a.Annotations["description"],
			Labels:      a.Labels,
			StartsAt:    a.StartsAt,
			EndsAt:      a.EndsAt,
		}

		s.log.Warn("alert", "status", a.Status, "name", alert.Name, "severity", alert.Severity, "summary", alert.Summary)
		metrics.AlertsReceived.WithLabelValues(a.Status).Inc()
		if err := s.db.UpsertAlert(r.Context(), alert); err != nil {

			s.log.Error("store alert", "err", err)
			writeError(w, http.StatusServiceUnavailable, "alert storage unavailable")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	alerts, err := s.db.ListAlerts(r.Context(), 100)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}
