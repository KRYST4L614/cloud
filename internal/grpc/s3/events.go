package s3grpc

import (
	"context"
	"net/url"

	cloudv1 "cloud/gen/go/cloud/v1"
	"cloud/internal/domain"
)

func (s *Handler) ReceiveStorageEvents(ctx context.Context, req *cloudv1.ReceiveStorageEventsRequest) (*cloudv1.ReceiveStorageEventsResponse, error) {
	notification := domain.StorageNotification{Event: req.GetNotification().GetEvent(), Records: make([]domain.StorageEvent, 0, len(req.GetNotification().GetRecords()))}
	for _, record := range req.GetNotification().GetRecords() {
		key := record.GetS3().GetObject().GetKey()
		if decoded, err := url.QueryUnescape(key); err == nil {
			key = decoded
		}
		notification.Records = append(notification.Records, domain.StorageEvent{Name: record.GetEventName(), Bucket: record.GetS3().GetBucket().GetName(), Key: key})
	}
	if err := s.service.ReceiveStorageEvents(ctx, notification); err != nil {
		return nil, s.failure(err)
	}
	return &cloudv1.ReceiveStorageEventsResponse{}, nil
}
