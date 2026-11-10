package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// An upgrade leaves a copy of the database as it was; a fresh database, and
// one already up to date, don't.
func TestBackupBeforeUpgrade(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.before-v*")); len(m) != 0 {
		t.Fatalf("a new database was backed up: %v", m)
	}
	ctx := context.Background()
	u := &User{Username: "ann", DisplayName: "Ann"}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	// Pretend the last migration hasn't run yet.
	last := len(migrations)
	if _, err := d.ExecContext(ctx, `DELETE FROM schema_version WHERE v=?`, last); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `ALTER TABLE users DROP COLUMN tokens_valid_after`); err != nil {
		t.Fatal(err)
	}
	for _, ix := range []string{"watch_state_item", "watchlist_item", "collection_items_item"} {
		if _, err := d.ExecContext(ctx, `DROP INDEX `+ix); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()

	d, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	bak := filepath.Join(dir, "wrentv.db.before-v"+itoa(last-1))
	if st, err := os.Stat(bak); err != nil || st.Size() == 0 {
		t.Fatalf("no backup at %s: %v", bak, err)
	}
	if got, err := d.UserByID(ctx, u.ID); err != nil || got.Username != "ann" {
		t.Fatalf("user after the upgrade: %+v %v", got, err)
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
