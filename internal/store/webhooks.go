package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Webhook struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	Events       []string  `json:"events"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	LastStatus   string    `json:"last_status,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	PendingCount int       `json:"pending_count"`
}

type WebhookDelivery struct {
	ID            int64      `json:"id"`
	EventID       int64      `json:"event_id"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LastError     string     `json:"last_error"`
	DeliveredAt   *time.Time `json:"delivered_at"`
}

type ClaimedWebhookDelivery struct {
	WebhookDelivery
	WebhookID  int64       `json:"-"`
	URL        string      `json:"-"`
	Secret     string      `json:"-"`
	LeaseToken string      `json:"-"`
	Event      ChangeEvent `json:"-"`
}

func (s *Store) CreateWebhook(ctx context.Context, name, target string, subscriptions []string, secret string, now time.Time) (Webhook, error) {
	if err := s.protectWebhookSecrets(ctx); err != nil {
		return Webhook{}, err
	}
	body, err := json.Marshal(subscriptions)
	if err != nil {
		return Webhook{}, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO webhook(name, url, events, secret, created_at) VALUES (?, ?, ?, ?, ?)`,
		name, target, string(body), secret, now.UTC().Format(time.RFC3339))
	if err != nil {
		return Webhook{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Webhook{}, err
	}
	return s.WebhookByID(ctx, id)
}

func (s *Store) protectWebhookSecrets(ctx context.Context) error {
	var sequence int
	var name, filename string
	if err := s.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&sequence, &name, &filename); err != nil {
		return err
	}
	if filename == "" {
		return errors.New("webhook secrets require a private database file")
	}
	if err := os.Chmod(filepath.Dir(filename), 0o700); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(filename+suffix, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

const webhookSelect = `SELECT w.id, w.name, w.url, w.events, w.enabled, w.created_at,
	COALESCE((SELECT status FROM webhook_delivery WHERE webhook_id = w.id AND attempts > 0 ORDER BY id DESC LIMIT 1), ''),
	COALESCE((SELECT last_error FROM webhook_delivery WHERE webhook_id = w.id AND attempts > 0 ORDER BY id DESC LIMIT 1), ''),
	(SELECT COUNT(*) FROM webhook_delivery WHERE webhook_id = w.id AND status IN ('pending', 'in_flight'))
	FROM webhook w`

func scanWebhook(scanner taskScanner) (Webhook, error) {
	var hook Webhook
	var subscriptions, createdAt string
	if err := scanner.Scan(&hook.ID, &hook.Name, &hook.URL, &subscriptions, &hook.Enabled, &createdAt,
		&hook.LastStatus, &hook.LastError, &hook.PendingCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return hook, ErrNotFound
		}
		return hook, err
	}
	if err := json.Unmarshal([]byte(subscriptions), &hook.Events); err != nil {
		return hook, err
	}
	var err error
	hook.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	return hook, err
}

func (s *Store) WebhookByID(ctx context.Context, id int64) (Webhook, error) {
	return scanWebhook(s.db.QueryRowContext(ctx, webhookSelect+" WHERE w.id = ?", id))
}

func (s *Store) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	rows, err := s.db.QueryContext(ctx, webhookSelect+" ORDER BY w.id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]Webhook, 0)
	for rows.Next() {
		hook, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, hook)
	}
	return result, rows.Err()
}

func (s *Store) UpdateWebhook(ctx context.Context, id int64, name, target string, subscriptions []string, enabled bool) error {
	body, err := json.Marshal(subscriptions)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE webhook SET name = ?, url = ?, events = ?, enabled = ? WHERE id = ?",
		name, target, string(body), enabled, id)
	return webhookAffected(result, err)
}

func (s *Store) DeleteWebhook(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM webhook WHERE id = ?", id)
	return webhookAffected(result, err)
}

func webhookAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) WebhookDeliveries(ctx context.Context, id int64) ([]WebhookDelivery, error) {
	if _, err := s.WebhookByID(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, event_id, status, attempts, next_attempt_at, last_error, delivered_at
		FROM webhook_delivery WHERE webhook_id = ? ORDER BY id DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]WebhookDelivery, 0)
	for rows.Next() {
		var delivery WebhookDelivery
		var nextAttempt string
		var delivered sql.NullString
		if err := rows.Scan(&delivery.ID, &delivery.EventID, &delivery.Status, &delivery.Attempts, &nextAttempt, &delivery.LastError, &delivered); err != nil {
			return nil, err
		}
		delivery.NextAttemptAt, err = time.Parse(time.RFC3339, nextAttempt)
		if err != nil {
			return nil, err
		}
		if delivered.Valid {
			parsed, err := time.Parse(time.RFC3339, delivered.String)
			if err != nil {
				return nil, err
			}
			delivery.DeliveredAt = &parsed
		}
		result = append(result, delivery)
	}
	return result, rows.Err()
}

func (s *Store) ClaimWebhookDelivery(ctx context.Context, now time.Time) (ClaimedWebhookDelivery, error) {
	var claimed ClaimedWebhookDelivery
	lease := make([]byte, 16)
	if _, err := rand.Read(lease); err != nil {
		return claimed, err
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return claimed, err
	}
	defer func() { _ = transaction.Rollback() }()
	stamp := now.UTC().Format(time.RFC3339)
	if _, err := transaction.ExecContext(ctx, `UPDATE webhook_delivery
		SET status = CASE WHEN attempts >= 5 THEN 'failed' ELSE 'pending' END,
		last_error = 'delivery interrupted', lease_token = NULL, lease_until = NULL
		WHERE status = 'in_flight' AND lease_until <= ?`, stamp); err != nil {
		return claimed, err
	}
	claimed.LeaseToken = hex.EncodeToString(lease)
	var createdAt string
	err = transaction.QueryRowContext(ctx, `SELECT d.id, d.event_id, d.attempts, w.id, w.url, w.secret,
		e.resource, e.resource_id, e.kind, e.created_at
		FROM webhook_delivery d JOIN webhook w ON w.id = d.webhook_id JOIN change_event e ON e.id = d.event_id
		WHERE d.status = 'pending' AND d.next_attempt_at <= ? AND d.attempts < 5 AND w.enabled = 1
		ORDER BY d.next_attempt_at, d.id LIMIT 1`, stamp).Scan(
		&claimed.ID, &claimed.EventID, &claimed.Attempts, &claimed.WebhookID, &claimed.URL, &claimed.Secret,
		&claimed.Event.Resource, &claimed.Event.ResourceID, &claimed.Event.Kind, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		if err := transaction.Commit(); err != nil {
			return claimed, err
		}
		return claimed, ErrNotFound
	}
	if err != nil {
		return claimed, err
	}
	claimed.Event.ID = claimed.EventID
	claimed.Event.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return claimed, err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE webhook_delivery SET status = 'in_flight', attempts = attempts + 1,
		lease_until = ?, lease_token = ? WHERE id = ?`, now.Add(30*time.Second).UTC().Format(time.RFC3339), claimed.LeaseToken, claimed.ID); err != nil {
		return claimed, err
	}
	claimed.Attempts++
	claimed.Status = "in_flight"
	return claimed, transaction.Commit()
}

func (s *Store) WebhookDeliveryActive(ctx context.Context, claimed ClaimedWebhookDelivery) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_delivery d JOIN webhook w ON w.id = d.webhook_id
		WHERE d.id = ? AND d.status = 'in_flight' AND d.lease_token = ? AND w.enabled = 1)`, claimed.ID, claimed.LeaseToken).Scan(&active)
	return active, err
}

func (s *Store) FinishWebhookDelivery(ctx context.Context, claimed ClaimedWebhookDelivery, success bool, message string, now time.Time) error {
	status := "pending"
	var deliveredAt any
	nextAttempt := now.UTC()
	if success {
		status = "delivered"
		deliveredAt = nextAttempt.Format(time.RFC3339)
		message = ""
	} else if claimed.Attempts >= 5 {
		status = "failed"
	} else {
		nextAttempt = nextAttempt.Add(30 * time.Second * time.Duration(1<<(claimed.Attempts-1)))
	}
	result, err := s.db.ExecContext(ctx, `UPDATE webhook_delivery SET status = ?, next_attempt_at = ?, last_error = ?, delivered_at = ?,
		lease_until = NULL, lease_token = NULL WHERE id = ? AND status = 'in_flight' AND lease_token = ?`,
		status, nextAttempt.Format(time.RFC3339), message, deliveredAt, claimed.ID, claimed.LeaseToken)
	return webhookAffected(result, err)
}
