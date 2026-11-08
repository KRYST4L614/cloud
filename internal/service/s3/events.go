package s3

import (
	"context"
	"errors"
	"strings"

	"cloud/internal/domain"
	"cloud/internal/metrics"
)

func (s *Service) ListAlerts(ctx context.Context) ([]domain.Alert, error) {
	return s.db.ListAlerts(ctx, 100)
}

func (s *Service) ReceiveAlerts(ctx context.Context, alerts []domain.Alert) error {
	for i := range alerts {
		alert := &alerts[i]
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
