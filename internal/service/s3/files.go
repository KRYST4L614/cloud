package s3

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"cloud/internal/domain"
	"cloud/internal/metrics"
)

var errTooLarge = errors.New("upload exceeds allowed size")

type limitedReader struct {
	io.Reader
	n, limit int64
}

func (r *limitedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += int64(n)
	if r.n > r.limit {
		return n, errTooLarge
	}
	return n, err
}

func (s *Service) ListFiles(ctx context.Context, owner, query string) ([]domain.File, error) {
	return s.db.ListFiles(ctx, owner, query)
}

func (s *Service) Upload(ctx context.Context, owner string, upload domain.Upload) (*domain.File, error) {
	user, err := s.db.UserByID(ctx, owner)
	if err != nil {
		return nil, err
	}
	limit := min(user.QuotaBytes-user.UsedBytes, s.cfg.MaxUploadBytes)
	if limit <= 0 {
		return nil, domain.ErrQuotaExceeded
	}
	name, ok := cleanName(upload.Name)
	if !ok {
		return nil, invalid("Invalid file name")
	}
	if upload.Body == nil {
		return nil, invalid("File body is required")
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	randomID := make([]byte, 12)
	if _, err = rand.Read(randomID); err != nil {
		return nil, err
	}
	file := &domain.File{ID: hex.EncodeToString(randomID), OwnerID: owner, Name: name, ContentType: contentType, CreatedAt: time.Now().UTC()}
	file.ObjectKey = "users/" + owner + "/" + file.ID
	reader := &limitedReader{Reader: upload.Body, limit: limit}
	if err = s.store.Put(ctx, file.ObjectKey, contentType, reader); err != nil {
		s.cleanupObject(file.ObjectKey)
		if errors.Is(err, errTooLarge) || reader.n > limit {
			return nil, &domain.Error{Kind: domain.ErrQuotaExceeded, Message: "File exceeds the size limit or remaining quota"}
		}
		return nil, err
	}
	file.Size = reader.n
	if err = s.db.ReserveSpace(ctx, owner, file.Size); err != nil {
		s.cleanupObject(file.ObjectKey)
		return nil, err
	}
	if err = s.db.CreateFile(ctx, file); err != nil {
		if releaseErr := s.db.ReleaseSpace(context.WithoutCancel(ctx), owner, file.Size); releaseErr != nil {
			s.log.Error("release upload quota", "err", releaseErr)
		}
		s.cleanupObject(file.ObjectKey)
		return nil, err
	}
	metrics.UploadedBytes.Add(float64(file.Size))
	return file, nil
}

func (s *Service) Download(ctx context.Context, owner, id string) (*domain.Download, error) {
	file, err := s.ownedFile(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	return s.openFile(ctx, file)
}

func (s *Service) PublicDownload(ctx context.Context, token string) (*domain.Download, error) {
	if token == "" {
		return nil, domain.ErrNotFound
	}
	file, err := s.db.FileByShareToken(ctx, token)
	if err != nil {
		return nil, err
	}
	return s.openFile(ctx, file)
}

func (s *Service) openFile(ctx context.Context, file *domain.File) (*domain.Download, error) {
	body, err := s.store.Get(ctx, file.ObjectKey)
	if errors.Is(err, domain.ErrNotFound) {
		if syncErr := s.forgetObject(ctx, file.ObjectKey, "download"); syncErr != nil {
			s.log.Error("forget missing object", "err", syncErr)
		}
		return nil, &domain.Error{Kind: domain.ErrNotFound, Message: "File no longer exists in storage"}
	}
	if err != nil {
		return nil, err
	}
	return &domain.Download{File: file, Body: &downloadReader{ReadCloser: body, remaining: file.Size}}, nil
}

type downloadReader struct {
	io.ReadCloser
	remaining int64
}

func (r *downloadReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	metrics.DownloadedBytes.Add(float64(n))
	if errors.Is(err, io.EOF) && r.remaining != 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (s *Service) Rename(ctx context.Context, owner, id, name string) error {
	if !validID(id) {
		return domain.ErrNotFound
	}
	name, ok := cleanName(name)
	if !ok {
		return invalid("Invalid file name")
	}
	return s.db.RenameFile(ctx, owner, id, name)
}

func (s *Service) Delete(ctx context.Context, owner, id string) error {
	file, err := s.ownedFile(ctx, owner, id)
	if err != nil {
		return err
	}
	if err = s.store.Delete(ctx, file.ObjectKey); err != nil {
		return err
	}
	deleted, err := s.db.DeleteFile(ctx, owner, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.db.ReleaseSpace(context.WithoutCancel(ctx), deleted.OwnerID, deleted.Size)
}

func (s *Service) Share(ctx context.Context, owner, id string) (*domain.ShareLink, error) {
	file, err := s.ownedFile(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if file.ShareToken == "" {
		data := make([]byte, 16)
		if _, err = rand.Read(data); err != nil {
			return nil, err
		}
		file.ShareToken = hex.EncodeToString(data)
		if err = s.db.SetShareToken(ctx, owner, id, file.ShareToken); err != nil {
			return nil, err
		}
	}
	return &domain.ShareLink{Token: file.ShareToken, URL: "/api/public/" + file.ShareToken}, nil
}

func (s *Service) Unshare(ctx context.Context, owner, id string) error {
	if !validID(id) {
		return domain.ErrNotFound
	}
	return s.db.SetShareToken(ctx, owner, id, "")
}

func (s *Service) ownedFile(ctx context.Context, owner, id string) (*domain.File, error) {
	if !validID(id) {
		return nil, domain.ErrNotFound
	}
	return s.db.FileByID(ctx, owner, id)
}

func (s *Service) cleanupObject(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.store.Delete(ctx, key); err != nil {
		s.log.Warn("cleanup object", "key", key, "err", err)
	}
}

func cleanName(name string) (string, bool) {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == "/" || len(name) > 255 || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n") {
		return "", false
	}
	return name, true
}
