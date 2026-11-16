package store

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/today/tack/migrations"
)

func TestTokenExpiryMigrationPreservesV9Credentials(t *testing.T) {
	st, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.db.ExecContext(t.Context(), `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	names := []string{"0001_init.sql", "0002_m1_core.sql", "0003_token_resources.sql", "0004_agent.sql", "0005_passkeys.sql", "0006_events.sql", "0007_resource_identity.sql", "0008_agent_replay.sql", "0009_task_identity.sql"}
	for index, name := range names {
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.applyMigration(t.Context(), index+1, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	for index, revoked := range []any{nil, "2026-11-16T10:00:00Z"} {
		if _, err := st.db.ExecContext(t.Context(), `INSERT INTO api_token(name, prefix, hash, scopes, project_ids, tag_ids, created_at, last_used_at, revoked_at)
		VALUES ('existing', 'tk_old', ?, '["capture"]', '[1,2]', '[3]', '2026-11-16T09:00:00Z', '2026-11-16T09:30:00Z', ?)`, fmt.Sprint(index), revoked); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		token, err := st.APITokenByID(t.Context(), int64(index+1))
		if err != nil {
			t.Fatal(err)
		}
		if token.Hash != fmt.Sprint(index) || token.ExpiresAt != nil || !slices.Equal(token.ProjectIDs, []int64{1, 2}) || !slices.Equal(token.TagIDs, []int64{3}) || !slices.Equal(token.Scopes, []string{"capture"}) || token.LastUsedAt == nil || token.LastUsedAt.Format(time.RFC3339) != "2026-11-16T09:30:00Z" || (token.RevokedAt != nil) != (index == 1) {
			t.Fatal("migration changed legacy credential or authorization")
		}
	}
}
