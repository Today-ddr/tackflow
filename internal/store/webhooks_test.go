package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookOutboxAtomicSubscriptionBoundary(t *testing.T) {
	st, _ := eventTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	if _, err := st.CreateTask(ctx, CreateTaskParams{Title: "before subscription", Now: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO webhook(name,url,events,secret,created_at) VALUES ('hook','https://example.com','["task.*"]','secret',?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProject(ctx, "not subscribed", "sage", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTask(ctx, CreateTaskParams{Title: "after subscription", Now: now}); err != nil {
		t.Fatal(err)
	}
	transaction, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO task_event(task_id,kind,created_at) VALUES (1,'commented',?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	var deliveries int
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_delivery").Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 {
		t.Fatalf("deliveries = %d, want one committed event after subscription", deliveries)
	}
	if _, err := st.db.ExecContext(ctx, "UPDATE webhook SET enabled = 0"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := st.db.QueryRowContext(ctx, "SELECT status FROM webhook_delivery").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("disabled webhook delivery = %s", status)
	}
	if _, err := st.AddComment(ctx, 1, "while disabled", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "UPDATE webhook SET enabled = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddComment(ctx, 1, "after enable", now); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_delivery WHERE status = 'pending'").Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 {
		t.Fatalf("reenable queued %d deliveries, want no backfill", deliveries)
	}
	if _, err := st.db.ExecContext(ctx, "DELETE FROM webhook"); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_delivery").Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 {
		t.Fatal("deleted webhook retained deliveries")
	}
}

func TestWebhookClaimRecoveryAndSecretPermissions(t *testing.T) {
	st, directory := eventTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	hook, err := st.CreateWebhook(ctx, "hook", "https://example.com", []string{"task.*"}, "signing secret", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(filepath.Join(directory, "tack.db"+suffix))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("database%s permissions: info=%v err=%v", suffix, info, err)
		}
	}
	if _, err := st.CreateTask(ctx, CreateTaskParams{Title: "task", Now: now}); err != nil {
		t.Fatal(err)
	}
	first, err := st.ClaimWebhookDelivery(ctx, now)
	if err != nil || first.Attempts != 1 || first.Secret != "signing secret" {
		t.Fatalf("claim = %+v, err = %v", first.WebhookDelivery, err)
	}
	if _, err := st.ClaimWebhookDelivery(ctx, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double claim: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	recovered, err := reopened.ClaimWebhookDelivery(ctx, now.Add(time.Minute))
	if err != nil || recovered.ID != first.ID || recovered.Attempts != 2 || recovered.LeaseToken == first.LeaseToken {
		t.Fatalf("recovered = %+v, err = %v", recovered.WebhookDelivery, err)
	}
	if err := reopened.FinishWebhookDelivery(ctx, first, true, "", now.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale lease wrote result: %v", err)
	}
	if err := reopened.UpdateWebhook(ctx, hook.ID, hook.Name, hook.URL, hook.Events, false); err != nil {
		t.Fatal(err)
	}
	if active, err := reopened.WebhookDeliveryActive(ctx, recovered); err != nil || active {
		t.Fatalf("disabled claim remains active = %v, err = %v", active, err)
	}
	if err := reopened.FinishWebhookDelivery(ctx, recovered, true, "", now.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled lease wrote result: %v", err)
	}
	list, err := reopened.WebhookDeliveries(ctx, hook.ID)
	if err != nil || len(list) != 1 || list[0].Status != "cancelled" {
		t.Fatalf("deliveries = %+v, err = %v", list, err)
	}
}

func TestWebhookConcurrentClaimAndCrashExhaustion(t *testing.T) {
	st, directory := eventTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	hook, err := st.CreateWebhook(ctx, "hook", "https://example.com", []string{"*"}, "secret", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTask(ctx, CreateTaskParams{Title: "task", Now: now}); err != nil {
		t.Fatal(err)
	}
	other, err := Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	results := make(chan error, 2)
	for _, connection := range []*Store{st, other} {
		go func() { _, err := connection.ClaimWebhookDelivery(ctx, now); results <- err }()
	}
	claimed, missing := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			claimed++
		} else if errors.Is(err, ErrNotFound) {
			missing++
		} else {
			t.Fatal(err)
		}
	}
	if claimed != 1 || missing != 1 {
		t.Fatalf("concurrent claims=%d missing=%d", claimed, missing)
	}
	for attempt := 2; attempt <= 5; attempt++ {
		now = now.Add(time.Minute)
		claim, err := other.ClaimWebhookDelivery(ctx, now)
		if err != nil || claim.Attempts != attempt {
			t.Fatalf("recovery %d: %+v err=%v", attempt, claim.WebhookDelivery, err)
		}
	}
	now = now.Add(time.Minute)
	if _, err := st.ClaimWebhookDelivery(ctx, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sixth crash recovery: %v", err)
	}
	deliveries, err := st.WebhookDeliveries(ctx, hook.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != "failed" || deliveries[0].Attempts != 5 {
		t.Fatalf("exhausted crash recovery: %+v err=%v", deliveries, err)
	}
}

func TestWebhookDestinationChangeCancelsOldDeliveries(t *testing.T) {
	st, _ := eventTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	hook, err := st.CreateWebhook(ctx, "hook", "https://old.example.com", []string{"*"}, "secret", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTask(ctx, CreateTaskParams{Title: "old destination", Now: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWebhook(ctx, hook.ID, hook.Name, "https://new.example.com", []string{"tag.*"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTask(ctx, CreateTaskParams{Title: "new subscription excludes task", Now: now}); err != nil {
		t.Fatal(err)
	}
	tag, err := st.CreateTag(ctx, "new destination", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimWebhookDelivery(ctx, now)
	if err != nil || claim.URL != "https://new.example.com" || claim.Event.Resource != "tag" || claim.Event.ResourceID != tag.ID {
		t.Fatalf("new destination claim=%+v event=%+v err=%v", claim.WebhookDelivery, claim.Event, err)
	}
	if err := st.DeleteWebhook(ctx, hook.ID); err != nil {
		t.Fatal(err)
	}
	if active, err := st.WebhookDeliveryActive(ctx, claim); err != nil || active {
		t.Fatalf("deleted active=%v err=%v", active, err)
	}
}
