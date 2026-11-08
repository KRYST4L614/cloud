package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"

	"cloud/internal/metrics"
	"cloud/internal/repository/mongo"
	"cloud/internal/storage/s3"
)

var errTooLarge = errors.New("upload exceeds the allowed size")

type limitedCounter struct {
	r     io.Reader
	n     int64
	limit int64
}

func (l *limitedCounter) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.limit {
		return n, errTooLarge
	}
	return n, err
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	files, err := s.db.ListFiles(r.Context(), userID(r), r.URL.Query().Get("q"))
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	user, err := s.db.UserByID(r.Context(), uid)
	if err != nil {
		s.dbError(w, err)
		return
	}
	limit := min(user.QuotaBytes-user.UsedBytes, s.cfg.MaxUploadBytes)
	if limit <= 0 {
		writeError(w, http.StatusRequestEntityTooLarge, "storage quota exceeded")
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}
	var part *multipart.Part
	for {
		p, err := mr.NextPart()
		if err != nil {
			writeError(w, http.StatusBadRequest, "form field \"file\" is missing")
			return
		}
		if p.FormName() == "file" && p.FileName() != "" {
			part = p
			defer p.Close()
			break
		}
		p.Close()
	}

	name, ok := cleanName(part.FileName())
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	f := &mongo.File{
		ID:          bson.NewObjectID(),
		OwnerID:     uid,
		Name:        name,
		ContentType: contentType,
		CreatedAt:   time.Now().UTC(),
	}
	f.ObjectKey = "users/" + uid.Hex() + "/" + f.ID.Hex()

	body := &limitedCounter{r: part, limit: limit}
	if err := s.store.Put(r.Context(), f.ObjectKey, contentType, body); err != nil {
		s.cleanupObject(f.ObjectKey)
		if errors.Is(err, errTooLarge) || body.n > limit {
			writeError(w, http.StatusRequestEntityTooLarge, "file exceeds the size limit or remaining quota")
			return
		}
		s.internalError(w, err)
		return
	}
	f.Size = body.n

	if err := s.db.ReserveSpace(r.Context(), uid, f.Size); err != nil {
		s.cleanupObject(f.ObjectKey)
		if errors.Is(err, mongo.ErrQuotaExceeded) {
			writeError(w, http.StatusRequestEntityTooLarge, "storage quota exceeded")
			return
		}
		s.internalError(w, err)
		return
	}
	if err := s.db.CreateFile(r.Context(), f); err != nil {
		s.db.ReleaseSpace(context.WithoutCancel(r.Context()), uid, f.Size)
		s.cleanupObject(f.ObjectKey)
		s.internalError(w, err)
		return
	}
	metrics.UploadedBytes.Add(float64(f.Size))
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) cleanupObject(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.store.Delete(ctx, key); err != nil {
		s.log.Warn("cleanup object", "key", key, "err", err)
	}
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	f, ok := s.ownedFile(w, r)
	if !ok {
		return
	}
	s.streamFile(w, r, f, r.URL.Query().Get("inline") == "1")
}

func (s *Server) publicDownload(w http.ResponseWriter, r *http.Request) {
	f, err := s.db.FileByShareToken(r.Context(), r.PathValue("token"))
	if err != nil {
		s.dbError(w, err)
		return
	}
	s.streamFile(w, r, f, false)
}

func (s *Server) streamFile(w http.ResponseWriter, r *http.Request, f *mongo.File, inline bool) {
	body, err := s.store.Get(r.Context(), f.ObjectKey)
	if errors.Is(err, s3.ErrNotFound) {

		if err := s.forgetObject(r.Context(), f.ObjectKey, "download"); err != nil {
			s.log.Error("forget missing object", "key", f.ObjectKey, "err", err)
		}
		writeError(w, http.StatusNotFound, "file no longer exists in storage")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer body.Close()

	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	n, err := io.Copy(w, body)
	metrics.DownloadedBytes.Add(float64(n))
	if err != nil {
		s.log.Warn("stream file", "file", f.ID.Hex(), "err", err)
	}
}

func (s *Server) renameFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name, ok := cleanName(req.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}
	if err := s.db.RenameFile(r.Context(), userID(r), id, name); err != nil {
		s.dbError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	f, ok := s.ownedFile(w, r)
	if !ok {
		return
	}
	if err := s.store.Delete(r.Context(), f.ObjectKey); err != nil {
		s.internalError(w, err)
		return
	}
	deleted, err := s.db.DeleteFile(r.Context(), f.OwnerID, f.ID)
	if errors.Is(err, mongo.ErrNotFound) {

		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.db.ReleaseSpace(r.Context(), deleted.OwnerID, deleted.Size); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) shareFile(w http.ResponseWriter, r *http.Request) {
	f, ok := s.ownedFile(w, r)
	if !ok {
		return
	}
	if f.ShareToken == "" {
		b := make([]byte, 16)
		rand.Read(b)
		f.ShareToken = hex.EncodeToString(b)
		if err := s.db.SetShareToken(r.Context(), f.OwnerID, f.ID, f.ShareToken); err != nil {
			s.dbError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"share_token": f.ShareToken, "url": "/api/public/" + f.ShareToken})
}

func (s *Server) unshareFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.db.SetShareToken(r.Context(), userID(r), id, ""); err != nil {
		s.dbError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ownedFile(w http.ResponseWriter, r *http.Request) (*mongo.File, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	f, err := s.db.FileByID(r.Context(), userID(r), id)
	if err != nil {
		s.dbError(w, err)
		return nil, false
	}
	return f, true
}

func pathID(w http.ResponseWriter, r *http.Request) (bson.ObjectID, bool) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return id, false
	}
	return id, true
}

func cleanName(name string) (string, bool) {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == "/" || len(name) > 255 || !utf8.ValidString(name) {
		return "", false
	}
	return name, true
}
