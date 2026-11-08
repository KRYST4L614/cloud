package monitoring

import (
	"context"
	"log/slog"
	"time"

	"cloud/internal/domain"
	"cloud/internal/metrics"
)

type Database interface {
	Ping(context.Context) error
	Stats(context.Context) (domain.Stats, error)
}

type ObjectStore interface {
	Ping(context.Context) error
}

type Monitor struct {
	db       Database
	store    ObjectStore
	interval time.Duration
	log      *slog.Logger
}

func New(db Database, store ObjectStore, interval time.Duration, log *slog.Logger) *Monitor {
	return &Monitor{db: db, store: store, interval: interval, log: log}
}

func (s *Monitor) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		s.checkDependencies(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Monitor) checkDependencies(ctx context.Context) {
	check := func(name string, ping func(context.Context) error) bool {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		start := time.Now()
		err := ping(ctx)
		metrics.DependencyLatency.WithLabelValues(name).Set(time.Since(start).Seconds())
		if err != nil {
			s.log.Warn("dependency check failed", "dependency", name, "err", err)
			metrics.DependencyUp.WithLabelValues(name).Set(0)
			return false
		}
		metrics.DependencyUp.WithLabelValues(name).Set(1)
		return true
	}
	check("s3", s.store.Ping)
	if !check("mongodb", s.db.Ping) {
		return
	}
	st, err := s.db.Stats(ctx)
	if err != nil {
		s.log.Warn("collect stats", "err", err)
		return
	}
	metrics.Users.Set(float64(st.Users))
	metrics.Files.Set(float64(st.Files))
	metrics.UsedBytes.Set(float64(st.UsedBytes))
	metrics.QuotaBytes.Set(float64(st.QuotaBytes))
}
