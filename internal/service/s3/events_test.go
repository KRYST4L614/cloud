package s3

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"

	"cloud/internal/domain"
)

const (
	alertOwner = "000000000000000000000001"
	otherOwner = "000000000000000000000002"
)

type alertRepository struct {
	Repository
	users     map[string]*domain.User
	alerts    []domain.Alert
	queried   string
	userError error
}

func (r *alertRepository) UserByID(_ context.Context, owner string) (*domain.User, error) {
	if r.userError != nil {
		return nil, r.userError
	}
	user, ok := r.users[owner]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return user, nil
}

func (r *alertRepository) ListAlerts(_ context.Context, owner string, _ int64) ([]domain.Alert, error) {
	r.queried = owner
	// Deliberately return unrelated records too, to check the service boundary.
	return r.alerts, nil
}

func (r *alertRepository) UpsertAlert(_ context.Context, alert *domain.Alert) error {
	r.alerts = append(r.alerts, *alert)
	return nil
}

func TestStorageUsageAlertThreshold(t *testing.T) {
	for _, tc := range []struct {
		name        string
		used, quota int64
		want        bool
	}{
		{name: "empty", quota: 100},
		{name: "below", used: 79, quota: 100},
		{name: "exactly 80 percent", used: 80, quota: 100, want: true},
		{name: "above", used: 81, quota: 100, want: true},
		{name: "full", used: 100, quota: 100, want: true},
		{name: "zero quota", used: 1},
		{name: "negative quota", quota: -1},
		{name: "fractional threshold below", used: 4, quota: 6},
		{name: "fractional threshold above", used: 5, quota: 6, want: true},
		{name: "one GiB below", used: 858993459, quota: 1 << 30},
		{name: "one GiB above", used: 858993460, quota: 1 << 30, want: true},
		{name: "large quota", used: math.MaxInt64, quota: math.MaxInt64, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &alertRepository{users: map[string]*domain.User{
				alertOwner: {ID: alertOwner, UsedBytes: tc.used, QuotaBytes: tc.quota},
			}}
			service := &Service{db: repo}
			alerts, err := service.ListAlerts(context.Background(), alertOwner)
			if err != nil {
				t.Fatal(err)
			}
			if (len(alerts) == 1) != tc.want {
				t.Fatalf("got %d alerts, want firing = %v", len(alerts), tc.want)
			}
			if tc.want && (alerts[0].OwnerID != alertOwner || alerts[0].Name != "StorageUsageHigh" || alerts[0].Status != "firing") {
				t.Fatalf("unexpected alert: %+v", alerts[0])
			}
		})
	}
}

func TestListAlertsOwnerIsolationAndRecovery(t *testing.T) {
	repo := &alertRepository{
		users: map[string]*domain.User{
			alertOwner: {ID: alertOwner, UsedBytes: 80, QuotaBytes: 100},
			otherOwner: {ID: otherOwner, UsedBytes: 1, QuotaBytes: 100},
		},
		alerts: []domain.Alert{
			{Name: "GlobalInfrastructure"},
			{Name: "StorageUsageHigh"},
			{OwnerID: alertOwner, Name: "StorageUsageHigh", Status: "resolved"},
			{OwnerID: alertOwner, Name: "PersonalEvent"},
			{OwnerID: otherOwner, Name: "OtherPersonalEvent"},
		},
	}
	service := &Service{db: repo}
	alerts, err := service.ListAlerts(context.Background(), alertOwner)
	if err != nil {
		t.Fatal(err)
	}
	if repo.queried != alertOwner || len(alerts) != 2 || alerts[0].Name != "StorageUsageHigh" || alerts[1].Name != "PersonalEvent" {
		t.Fatalf("unexpected first user's alerts: %+v (queried %s)", alerts, repo.queried)
	}
	id := alerts[0].ID
	alerts, err = service.ListAlerts(context.Background(), otherOwner)
	if err != nil {
		t.Fatal(err)
	}
	if repo.queried != otherOwner || len(alerts) != 1 || alerts[0].Name != "OtherPersonalEvent" {
		t.Fatalf("unexpected other user's alerts: %+v", alerts)
	}
	// Current usage is authoritative: recovery and recurrence need no timer or webhook.
	for _, used := range []int64{79, 90, 90} {
		repo.users[alertOwner].UsedBytes = used
		alerts, err = service.ListAlerts(context.Background(), alertOwner)
		if err != nil {
			t.Fatal(err)
		}
		if used < 80 {
			if len(alerts) != 1 || alerts[0].Name != "PersonalEvent" {
				t.Fatalf("quota warning survived recovery: %+v", alerts)
			}
		} else if len(alerts) != 2 || alerts[0].ID != id {
			t.Fatalf("quota warning duplicated or changed identity: %+v", alerts)
		}
	}
	_, err = service.ListAlerts(context.Background(), "unknown")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown user error = %v", err)
	}
}

func TestReceiveAlertsIgnoresQuotaAndScopesOwners(t *testing.T) {
	repo := &alertRepository{}
	service := &Service{db: repo, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err := service.ReceiveAlerts(context.Background(), []domain.Alert{
		{Name: "StorageUsageHigh", Labels: map[string]string{"user_id": alertOwner}},
		{Name: "PersonalEvent", Labels: map[string]string{"user_id": alertOwner}},
		{Name: "GlobalInfrastructure", OwnerID: otherOwner},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.alerts) != 2 || repo.alerts[0].OwnerID != alertOwner || repo.alerts[1].OwnerID != "" {
		t.Fatalf("unexpected stored webhook alerts: %+v", repo.alerts)
	}
}
