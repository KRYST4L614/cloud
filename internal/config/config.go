package config

import (
	"os"
	"strconv"
	"time"
)

// Config contains backend settings loaded from process environment variables.
type Config struct {
	HTTPAddr string
	GRPCAddr string

	// MongoDB stores accounts, file metadata, quota usage, and alerts.
	MongoURI string
	MongoDB  string

	// S3 settings select the object storage endpoint, credentials, and bucket.
	S3Endpoint  string
	S3Region    string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string

	// JWTSecret signs access tokens; JWTTTL controls their lifetime.
	JWTSecret string
	JWTTTL    time.Duration

	// Quota applies to new users; the upload limit applies to each file.
	UserQuotaBytes int64
	MaxUploadBytes int64
	// Separate shared secrets authenticate Grafana notifications and S3 events.
	AlertWebhookToken string
	S3EventToken      string
	// Control the interval between background MongoDB and S3 health checks.
	DependencyInterval time.Duration
}

// Load reads environment variables with local development defaults.
// Compose loads .env; this package does not read it directly.
func Load() Config {
	return Config{
		HTTPAddr:           env("HTTP_ADDR", ":8000"),
		GRPCAddr:           env("GRPC_ADDR", ":9000"),
		MongoURI:           env("MONGO_URI", "mongodb://localhost:27017"),
		MongoDB:            env("MONGO_DB", "cloud"),
		S3Endpoint:         env("S3_ENDPOINT", "http://localhost:7070"),
		S3Region:           env("S3_REGION", "us-east-1"),
		S3AccessKey:        env("S3_ACCESS_KEY", "admin"),
		S3SecretKey:        env("S3_SECRET_KEY", "adminsecret"),
		S3Bucket:           env("S3_BUCKET", "cloud-files"),
		JWTSecret:          env("JWT_SECRET", "change-me"),
		JWTTTL:             envDuration("JWT_TTL", 24*time.Hour),
		UserQuotaBytes:     envInt("USER_QUOTA_BYTES", 1<<30),
		MaxUploadBytes:     envInt("MAX_UPLOAD_BYTES", 512<<20),
		AlertWebhookToken:  env("ALERT_WEBHOOK_TOKEN", "change-me"),
		S3EventToken:       env("S3_EVENT_TOKEN", "change-me"),
		DependencyInterval: envDuration("DEPENDENCY_CHECK_INTERVAL", 10*time.Second),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envInt uses the default when the value is missing or not a valid integer.
func envInt(key string, def int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(key), 10, 64); err == nil {
		return v
	}
	return def
}

// envDuration accepts Go duration strings such as "24h" or "10s".
func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
