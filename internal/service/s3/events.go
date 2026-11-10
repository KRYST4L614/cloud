package s3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cloud/internal/domain"
	"cloud/internal/metrics"
)

func (s *Service) ListAlerts(ctx context.Context, owner string) ([]domain.Alert, error) {
	user, err := s.db.UserByID(ctx, owner)
	if err != nil {
		return nil, err
	}
	stored, err := s.db.ListAlerts(ctx, owner, 100)
	if err != nil {
		return nil, err
	}
	alerts := make([]domain.Alert, 0, len(stored)+1)
	// ceil(80% * quota), without rounding down or overflowing int64.
	if user.QuotaBytes > 0 && user.UsedBytes >= user.QuotaBytes-user.QuotaBytes/5 {
		id := "storage-usage-high:" + owner
		alerts = append(alerts, domain.Alert{
			ID: id, Fingerprint: id, OwnerID: owner,
			Name: "StorageUsageHigh", Status: "firing", Severity: "warning",
			Summary: "Your storage is at least 80% full",
			Description: fmt.Sprintf("You are using %d of %d bytes (%.1f%% of your quota). Delete files to free up space.",
				user.UsedBytes, user.QuotaBytes, 100*float64(user.UsedBytes)/float64(user.QuotaBytes)),
			Labels: map[string]string{"user_id": owner}, UpdatedAt: time.Now().UTC(),
		})
	}
	for _, alert := range stored {
		// Legacy global quota alerts must never replace the current personal condition.
		if alert.OwnerID == owner && alert.Name != "StorageUsageHigh" && len(alerts) < 100 {
			alerts = append(alerts, alert)
		}
	}
	return alerts, nil
}

func (s *Service) ReceiveAlerts(ctx context.Context, alerts []domain.Alert) error {
	for i := range alerts {
		alert := &alerts[i]
		// Quota warnings are derived from current usage, never from delayed webhooks.
		if alert.Name == "StorageUsageHigh" {
			continue
		}
		alert.OwnerID = ""
		if owner := alert.Labels["user_id"]; validID(owner) {
			alert.OwnerID = strings.ToLower(owner)
		}
		s.log.Warn("alert", "status", alert.Status, "name", alert.Name, "severity", alert.Severity, "summary", alert.Summary)
		metrics.AlertsReceived.WithLabelValues(alert.Status).Inc()
		if err := s.db.UpsertAlert(ctx, alert); err != nil {
			s.log.Error("store alert", "err", err)
			return &domain.Error{Kind: domain.ErrUnavailable, Message: "Alert storage unavailable"}
		}
	}
	return nil
}

func (s *Service) ReceiveStorageEvents(ctx context.Context, notification domain.StorageNotification) error {
	if notification.Event != "" {
		metrics.StorageEvents.WithLabelValues(notification.Event).Inc()
	}
	for _, record := range notification.Records {
		metrics.StorageEvents.WithLabelValues(record.Name).Inc()
		if record.Bucket != s.store.Bucket() || !strings.HasPrefix(record.Name, "s3:ObjectRemoved:") {
			continue
		}
		if err := s.forgetObject(ctx, record.Key, "event"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) forgetObject(ctx context.Context, key, source string) error {
	ctx = context.WithoutCancel(ctx)
	file, err := s.db.DeleteFileByKey(ctx, key)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = s.db.ReleaseSpace(ctx, file.OwnerID, file.Size); err != nil {
		return err
	}
	metrics.SyncRemovedFiles.WithLabelValues(source).Inc()
	s.log.Info("file removed outside the app", "source", source, "key", key, "file", file.ID, "name", file.Name)
	return nil
}
