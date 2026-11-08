package s3grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	cloudv1 "cloud/gen/go/cloud/v1"
	"cloud/internal/domain"
)

func (s *Handler) Register(ctx context.Context, req *cloudv1.RegisterRequest) (*cloudv1.RegisterResponse, error) {
	session, err := s.service.Register(ctx, domain.Credentials{Email: req.GetCredentials().GetEmail(), Password: req.GetCredentials().GetPassword()})
	if err != nil {
		return nil, s.failure(err)
	}
	httpStatus(ctx, "201")
	return &cloudv1.RegisterResponse{Session: sessionMessage(session)}, nil
}
func (s *Handler) Login(ctx context.Context, req *cloudv1.LoginRequest) (*cloudv1.LoginResponse, error) {
	session, err := s.service.Login(ctx, domain.Credentials{Email: req.GetCredentials().GetEmail(), Password: req.GetCredentials().GetPassword()})
	if err != nil {
		return nil, s.failure(err)
	}
	return &cloudv1.LoginResponse{Session: sessionMessage(session)}, nil
}
func (s *Handler) Me(ctx context.Context, _ *cloudv1.MeRequest) (*cloudv1.MeResponse, error) {
	user, err := s.service.Profile(ctx, userID(ctx))
	if err != nil {
		return nil, s.failure(err)
	}
	return &cloudv1.MeResponse{User: userMessage(user)}, nil
}
func sessionMessage(session *domain.Session) *cloudv1.Session {
	return &cloudv1.Session{Token: session.Token, ExpiresAt: timestamppb.New(session.ExpiresAt), User: userMessage(session.User)}
}
func userMessage(user *domain.User) *cloudv1.User {
	return &cloudv1.User{Id: user.ID, Email: user.Email, QuotaBytes: user.QuotaBytes, UsedBytes: user.UsedBytes, CreatedAt: timestamppb.New(user.CreatedAt)}
}
