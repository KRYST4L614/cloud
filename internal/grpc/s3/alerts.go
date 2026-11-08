package s3grpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	cloudv1 "cloud/gen/go/cloud/v1"
	"cloud/internal/domain"
)

func (s *Handler) ListAlerts(ctx context.Context, _ *cloudv1.ListAlertsRequest) (*cloudv1.ListAlertsResponse, error) {
	alerts, err := s.service.ListAlerts(ctx)
	if err != nil {
		return nil, s.failure(err)
	}
	response := &cloudv1.ListAlertsResponse{Alerts: make([]*cloudv1.Alert, 0, len(alerts))}
	for i := range alerts {
		response.Alerts = append(response.Alerts, alertMessage(&alerts[i]))
	}
	return response, nil
}
func (s *Handler) ReceiveAlerts(ctx context.Context, req *cloudv1.ReceiveAlertsRequest) (*cloudv1.ReceiveAlertsResponse, error) {
	alerts := make([]domain.Alert, 0, len(req.GetNotification().GetAlerts()))
	for _, message := range req.GetNotification().GetAlerts() {
		starts, err := timestampValue(message.GetStartsAt())
		if err != nil {
			return nil, err
		}
		ends, err := timestampValue(message.GetEndsAt())
		if err != nil {
			return nil, err
		}
		labels, annotations := message.GetLabels(), message.GetAnnotations()
		alerts = append(alerts, domain.Alert{Fingerprint: message.GetFingerprint(), Name: labels["alertname"], Status: message.GetStatus(), Severity: labels["severity"], Summary: annotations["summary"], Description: annotations["description"], Labels: labels, StartsAt: starts, EndsAt: ends})
	}
	if err := s.service.ReceiveAlerts(ctx, alerts); err != nil {
		return nil, s.failure(err)
	}
	httpStatus(ctx, "204")
	return &cloudv1.ReceiveAlertsResponse{}, nil
}
func timestampValue(timestamp *timestamppb.Timestamp) (time.Time, error) {
	if timestamp == nil {
		return time.Time{}, nil
	}
	if err := timestamp.CheckValid(); err != nil {
		return time.Time{}, status.Error(codes.InvalidArgument, "Invalid timestamp")
	}
	return timestamp.AsTime(), nil
}
func alertMessage(alert *domain.Alert) *cloudv1.Alert {
	return &cloudv1.Alert{Id: alert.ID, Fingerprint: alert.Fingerprint, Name: alert.Name, Status: alert.Status, Severity: alert.Severity, Summary: alert.Summary, Description: alert.Description, Labels: alert.Labels, StartsAt: timestamppb.New(alert.StartsAt), EndsAt: timestamppb.New(alert.EndsAt), UpdatedAt: timestamppb.New(alert.UpdatedAt)}
}
