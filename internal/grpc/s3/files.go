package s3grpc

import (
	"context"
	"errors"
	"io"
	"mime"
	"strconv"

	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	cloudv1 "cloud/gen/go/cloud/v1"
	"cloud/internal/domain"
)

const chunkSize = 64 << 10

type uploadReader struct {
	stream  grpc.ClientStreamingServer[cloudv1.UploadFileRequest, cloudv1.UploadFileResponse]
	pending []byte
}

func (r *uploadReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		message, err := r.stream.Recv()
		if err != nil {
			return 0, err
		}
		data, ok := message.Payload.(*cloudv1.UploadFileRequest_Data)
		if !ok {
			return 0, &domain.Error{Kind: domain.ErrInvalidArgument, Message: "Only data chunks may follow upload metadata"}
		}
		if len(data.Data) > chunkSize {
			return 0, &domain.Error{Kind: domain.ErrInvalidArgument, Message: "Upload chunks must not exceed 64 KiB"}
		}
		r.pending = data.Data
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (s *Handler) ListFiles(ctx context.Context, req *cloudv1.ListFilesRequest) (*cloudv1.ListFilesResponse, error) {
	files, err := s.service.ListFiles(ctx, userID(ctx), req.GetQ())
	if err != nil {
		return nil, s.failure(err)
	}
	response := &cloudv1.ListFilesResponse{Files: make([]*cloudv1.File, 0, len(files))}
	for i := range files {
		response.Files = append(response.Files, fileMessage(&files[i]))
	}
	return response, nil
}

func (s *Handler) UploadFile(stream grpc.ClientStreamingServer[cloudv1.UploadFileRequest, cloudv1.UploadFileResponse]) error {
	first, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return status.Error(codes.InvalidArgument, "Upload metadata is required")
	}
	if err != nil {
		return s.failure(err)
	}
	if first.GetMetadata() == nil {
		return status.Error(codes.InvalidArgument, "The first upload message must contain metadata")
	}
	file, err := s.service.Upload(stream.Context(), userID(stream.Context()), domain.Upload{Name: first.GetMetadata().GetName(), Body: &uploadReader{stream: stream}})
	if err != nil {
		return s.failure(err)
	}
	return stream.SendAndClose(&cloudv1.UploadFileResponse{File: fileMessage(file)})
}

func (s *Handler) DownloadFile(req *cloudv1.DownloadFileRequest, stream grpc.ServerStreamingServer[cloudv1.DownloadFileResponse]) error {
	download, err := s.service.Download(stream.Context(), userID(stream.Context()), req.GetId())
	if err != nil {
		return s.failure(err)
	}
	return s.sendDownload(download, req.GetInline(), stream.SendHeader, func(body *httpbody.HttpBody) error { return stream.Send(&cloudv1.DownloadFileResponse{Body: body}) })
}
func (s *Handler) PublicDownload(req *cloudv1.PublicDownloadRequest, stream grpc.ServerStreamingServer[cloudv1.PublicDownloadResponse]) error {
	download, err := s.service.PublicDownload(stream.Context(), req.GetToken())
	if err != nil {
		return s.failure(err)
	}
	return s.sendDownload(download, false, stream.SendHeader, func(body *httpbody.HttpBody) error { return stream.Send(&cloudv1.PublicDownloadResponse{Body: body}) })
}
func (s *Handler) sendDownload(download *domain.Download, inline bool, sendHeader func(metadata.MD) error, send func(*httpbody.HttpBody) error) error {
	defer download.Body.Close()
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	if err := sendHeader(metadata.Pairs(
		"content-disposition", mime.FormatMediaType(disposition, map[string]string{"filename": download.File.Name}),
		"content-length", strconv.FormatInt(download.File.Size, 10),
		"x-content-type-options", "nosniff", "content-security-policy", "sandbox",
	)); err != nil {
		return err
	}
	// An initial empty body carries the content type even for an empty file.
	if err := send(&httpbody.HttpBody{ContentType: download.File.ContentType}); err != nil {
		return err
	}
	buffer := make([]byte, chunkSize)
	for {
		n, err := download.Body.Read(buffer)
		if n > 0 {
			if sendErr := send(&httpbody.HttpBody{ContentType: download.File.ContentType, Data: buffer[:n]}); sendErr != nil {
				return sendErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return s.failure(err)
		}
	}
}

func (s *Handler) RenameFile(ctx context.Context, req *cloudv1.RenameFileRequest) (*cloudv1.RenameFileResponse, error) {
	if err := s.service.Rename(ctx, userID(ctx), req.GetId(), req.GetName()); err != nil {
		return nil, s.failure(err)
	}
	httpStatus(ctx, "204")
	return &cloudv1.RenameFileResponse{}, nil
}
func (s *Handler) DeleteFile(ctx context.Context, req *cloudv1.DeleteFileRequest) (*cloudv1.DeleteFileResponse, error) {
	if err := s.service.Delete(ctx, userID(ctx), req.GetId()); err != nil {
		return nil, s.failure(err)
	}
	httpStatus(ctx, "204")
	return &cloudv1.DeleteFileResponse{}, nil
}
func (s *Handler) ShareFile(ctx context.Context, req *cloudv1.ShareFileRequest) (*cloudv1.ShareFileResponse, error) {
	link, err := s.service.Share(ctx, userID(ctx), req.GetId())
	if err != nil {
		return nil, s.failure(err)
	}
	return &cloudv1.ShareFileResponse{ShareLink: &cloudv1.ShareLink{ShareToken: link.Token, Url: link.URL}}, nil
}
func (s *Handler) UnshareFile(ctx context.Context, req *cloudv1.UnshareFileRequest) (*cloudv1.UnshareFileResponse, error) {
	if err := s.service.Unshare(ctx, userID(ctx), req.GetId()); err != nil {
		return nil, s.failure(err)
	}
	httpStatus(ctx, "204")
	return &cloudv1.UnshareFileResponse{}, nil
}
func fileMessage(file *domain.File) *cloudv1.File {
	return &cloudv1.File{Id: file.ID, Name: file.Name, Size: file.Size, ContentType: file.ContentType, ShareToken: file.ShareToken, CreatedAt: timestamppb.New(file.CreatedAt)}
}
