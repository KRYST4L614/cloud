package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cloud_http_requests_total",
		Help: "HTTP requests processed by the backend.",
	}, []string{"method", "route", "status"})

	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cloud_http_request_duration_seconds",
		Help:    "HTTP request latency.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"method", "route"})

	UploadedBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cloud_storage_uploaded_bytes_total",
		Help: "Bytes uploaded to object storage.",
	})

	DownloadedBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cloud_storage_downloaded_bytes_total",
		Help: "Bytes downloaded from object storage.",
	})

	StorageOps = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cloud_storage_operations_total",
		Help: "Object storage operations by type and result.",
	}, []string{"op", "result"})

	AuthEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cloud_auth_events_total",
		Help: "Authentication events by type and result.",
	}, []string{"event", "result"})

	DependencyUp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cloud_dependency_up",
		Help: "Whether a dependency (s3, mongodb) is reachable: 1 = up, 0 = down.",
	}, []string{"dependency"})

	DependencyLatency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cloud_dependency_check_seconds",
		Help: "Duration of the last dependency health check.",
	}, []string{"dependency"})

	Users = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "cloud_users_total",
		Help: "Registered users.",
	})

	Files = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "cloud_files_total",
		Help: "Stored files.",
	})

	UsedBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "cloud_storage_used_bytes",
		Help: "Total bytes stored by all users.",
	})

	QuotaBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "cloud_storage_quota_bytes_total",
		Help: "Sum of all user quotas.",
	})

	StorageEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cloud_storage_events_total",
		Help: "Bucket event notifications received from VersityGW.",
	}, []string{"event"})

	SyncRemovedFiles = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cloud_storage_sync_removed_files_total",
		Help: "File records removed because the object disappeared from storage outside the app.",
	}, []string{"source"})

	AlertsReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cloud_alerts_received_total",
		Help: "Alert notifications received from Grafana.",
	}, []string{"status"})
)
