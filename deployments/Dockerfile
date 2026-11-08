# Build from the repository root to include backend sources and the embedded API specification.
FROM golang:1.26-alpine AS build
WORKDIR /src
# Cache module downloads separately from application source changes.
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY api ./api
# Build a standalone binary without CGO and strip debug information.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/backend ./cmd/server

# Keep the compiler and source code out of the runtime image.
FROM alpine:3.22
# CA certificates support HTTPS; wget is used by the health check.
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 app
COPY --from=build /out/backend /usr/local/bin/backend
# Run the application without root privileges.
USER app
EXPOSE 8000
# Check liveness, not S3 readiness: VersityGW starts after this check succeeds.
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s CMD wget -qO- http://127.0.0.1:8000/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/backend"]
