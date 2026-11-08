package httpapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	apispec "cloud/api"
	cloudv1 "cloud/gen/go/cloud/v1"
	"cloud/internal/metrics"
)

var gatewayJSON = runtime.JSONPb{MarshalOptions: protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}, UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: false}}

func newHandler(ctx context.Context, connection *grpc.ClientConn, ready http.HandlerFunc, log *slog.Logger) (http.Handler, error) {
	gateway := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &gatewayJSON),
		runtime.WithForwardResponseRewriter(func(_ context.Context, response proto.Message) (any, error) {
			if wrapped, ok := response.(interface{ XXX_ResponseBody() interface{} }); ok {
				if body, ok := wrapped.XXX_ResponseBody().(*httpbody.HttpBody); ok {
					return body, nil
				}
			}
			return response, nil
		}),
		runtime.WithMarshalerOption("application/json+webhook", &runtime.JSONPb{MarshalOptions: gatewayJSON.MarshalOptions, UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: true}}),
		runtime.WithMarshalerOption("application/octet-stream", &binaryMarshaler{HTTPBodyMarshaler: runtime.HTTPBodyMarshaler{Marshaler: &gatewayJSON}}),
		runtime.WithOutgoingHeaderMatcher(func(key string) (string, bool) {
			switch key {
			case "content-length", "content-disposition", "content-security-policy", "x-content-type-options":
				return key, true
			}
			return runtime.DefaultHeaderMatcher(key)
		}),
		runtime.WithMetadata(func(_ context.Context, r *http.Request) metadata.MD {
			if r.URL.Path == "/internal/s3-events" {
				return metadata.Pairs("authorization", "Bearer "+r.URL.Query().Get("token"))
			}
			if r.Header.Get("Authorization") == "" && (strings.HasSuffix(r.URL.Path, "/download")) && r.URL.Query().Get("access_token") != "" {
				return metadata.Pairs("authorization", "Bearer "+r.URL.Query().Get("access_token"))
			}
			return nil
		}),
		runtime.WithStreamErrorHandler(func(ctx context.Context, err error) *status.Status {
			if recorder, ok := ctx.Value(responseKey{}).(*statusRecorder); ok {
				if recorder.started {
					panic(http.ErrAbortHandler)
				}
				recorder.Header().Del("Content-Length")
				recorder.Header().Del("Content-Disposition")
			}
			return status.Convert(err)
		}),
		runtime.WithErrorHandler(func(_ context.Context, _ *runtime.ServeMux, _ runtime.Marshaler, w http.ResponseWriter, _ *http.Request, err error) {
			var httpErr *runtime.HTTPStatusError
			if errors.As(err, &httpErr) {
				writeError(w, httpErr.HTTPStatus, status.Convert(httpErr.Err).Message())
				return
			}
			writeRPCError(w, err)
		}),
		runtime.WithForwardResponseOption(func(ctx context.Context, w http.ResponseWriter, _ proto.Message) error {
			if w.Header().Get("Content-Length") != "" {
				w.Header().Del("Transfer-Encoding")
			}
			if md, ok := runtime.ServerMetadataFromContext(ctx); ok {
				if values := md.HeaderMD.Get("x-http-code"); len(values) > 0 {
					delete(md.HeaderMD, "x-http-code")
					w.Header().Del("Grpc-Metadata-X-Http-Code")
					code, err := strconv.Atoi(values[0])
					if err != nil {
						return err
					}
					w.WriteHeader(code)
				}
			}
			return nil
		}),
	)
	if err := cloudv1.RegisterCloudStorageHandler(ctx, gateway, connection); err != nil {
		return nil, err
	}
	client := cloudv1.NewCloudStorageClient(connection)
	mux := http.NewServeMux()
	mux.Handle("/api/", gateway)
	mux.HandleFunc("GET /api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(apispec.Spec)
	})
	mux.HandleFunc("POST /api/files", uploadHTTP(client))
	mux.Handle("POST /internal/s3-events", gateway)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", ready)
	mux.Handle("GET /metrics", promhttp.Handler())
	return instrument(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.URL.Path != "/api/files" && r.URL.Path != "/api/alerts/webhook" && r.URL.Path != "/internal/s3-events" {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		if r.URL.Path == "/api/alerts/webhook" || r.URL.Path == "/internal/s3-events" {
			r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
			r.Header.Set("Content-Type", "application/json+webhook")
		}
		if strings.HasPrefix(r.URL.Path, "/api/public/") || strings.HasSuffix(r.URL.Path, "/download") {
			r.Header.Set("Accept", "application/octet-stream")
		}
		mux.ServeHTTP(w, r)
	})), nil
}

func outgoingContext(r *http.Request, queryToken bool) context.Context {
	authorization := r.Header.Get("Authorization")
	if authorization == "" && queryToken && r.URL.Query().Get("access_token") != "" {
		authorization = "Bearer " + r.URL.Query().Get("access_token")
	}
	return metadata.NewOutgoingContext(r.Context(), metadata.Pairs("authorization", authorization))
}

func uploadHTTP(client cloudv1.CloudStorageClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			writeError(w, 400, "Expected multipart/form-data")
			return
		}
		for {
			part, err := reader.NextPart()
			if err != nil {
				writeError(w, 400, "Form field \"file\" is missing")
				return
			}
			if part.FormName() != "file" || part.FileName() == "" {
				if err = part.Close(); err != nil {
					writeError(w, 400, "Invalid multipart body")
					return
				}
				continue
			}
			ctx, cancel := context.WithCancel(outgoingContext(r, false))
			defer cancel()
			stream, err := client.UploadFile(ctx)
			if err != nil {
				writeRPCError(w, err)
				return
			}
			send := func(message *cloudv1.UploadFileRequest) bool {
				if sendErr := stream.Send(message); sendErr != nil {
					if _, rpcErr := stream.CloseAndRecv(); rpcErr != nil {
						sendErr = rpcErr
					}
					writeRPCError(w, sendErr)
					return false
				}
				return true
			}
			if !send(&cloudv1.UploadFileRequest{Payload: &cloudv1.UploadFileRequest_Metadata{Metadata: &cloudv1.UploadMetadata{Name: part.FileName()}}}) {
				return
			}
			buffer := make([]byte, 64<<10)
			for {
				n, readErr := part.Read(buffer)
				if n > 0 && !send(&cloudv1.UploadFileRequest{Payload: &cloudv1.UploadFileRequest_Data{Data: buffer[:n]}}) {
					return
				}
				if errors.Is(readErr, io.EOF) {
					break
				}
				if readErr != nil {
					writeError(w, 400, "Unable to read uploaded file")
					return
				}
			}
			file, err := stream.CloseAndRecv()
			if err != nil {
				writeRPCError(w, err)
				return
			}
			data, err := gatewayJSON.Marshal(file.GetFile())
			if err != nil {
				writeError(w, 500, "Internal error")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(data)
			return
		}
	}
}

type binaryMarshaler struct{ runtime.HTTPBodyMarshaler }

func (*binaryMarshaler) Delimiter() []byte { return nil }
func (m *binaryMarshaler) Marshal(value any) ([]byte, error) {
	if chunk, ok := value.(map[string]interface{}); ok {
		if message, ok := chunk["error"].(proto.Message); ok {
			data, err := protojson.Marshal(message)
			if err != nil {
				return nil, err
			}
			var decoded struct {
				Message string `json:"message"`
			}
			if err = json.Unmarshal(data, &decoded); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]string{"error": decoded.Message})
		}
	}
	return m.HTTPBodyMarshaler.Marshal(value)
}

type responseKey struct{}

func writeRPCError(w http.ResponseWriter, err error) {
	code := runtime.HTTPStatusFromCode(status.Code(err))
	if status.Code(err) == codes.ResourceExhausted {
		code = http.StatusRequestEntityTooLarge
	}
	writeError(w, code, status.Convert(err).Message())
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	started bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.started {
		return
	}
	r.started = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) Write(data []byte) (int, error) {
	if r.status == http.StatusNoContent {
		return len(data), nil
	}
	if !r.started {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(data)
}

func instrument(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), responseKey{}, rec)))
		route := httpRoute(r)
		if route == "/metrics" {
			return
		}
		metrics.HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		metrics.HTTPDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
		if route != "/healthz" {
			log.Info("request", "method", r.Method, "route", route, "status", rec.status, "duration", time.Since(start))
		}
	})
}

func httpRoute(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/api/files/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/files/"), "/")
		if len(parts) == 1 {
			return "/api/files/{id}"
		}
		if len(parts) == 2 && (parts[1] == "download" || parts[1] == "share") {
			return "/api/files/{id}/" + parts[1]
		}
		return "unmatched"
	}
	if strings.HasPrefix(r.URL.Path, "/api/public/") {
		return "/api/public/{token}"
	}
	switch r.URL.Path {
	case "/api/auth/register", "/api/auth/login", "/api/me", "/api/files", "/api/alerts", "/api/alerts/webhook", "/api/openapi.yaml", "/healthz", "/readyz", "/metrics", "/internal/s3-events":
		return r.URL.Path
	}
	return "unmatched"
}
