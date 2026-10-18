package main

import (
	"net/url"
	"os"
	"testing"

	"github.com/bradleymackey/track-slash/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.RunWithMigratedTemplate(m))
}

// TestApplyMigrationsAcceptsPoolSettingsInDatabaseURL covers DATABASE_URL
// carrying pgxpool's pool_* settings, which Postgres rejects as startup
// parameters.
func TestApplyMigrationsAcceptsPoolSettingsInDatabaseURL(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	u, err := url.Parse(db.URL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	q := u.Query()
	q.Set("pool_max_conns", "4")
	u.RawQuery = q.Encode()

	if err := applyMigrations(u.String()); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}
	var tables int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename = 'issues'`).Scan(&tables); err != nil {
		t.Fatalf("count issues table: %v", err)
	}
	if tables != 1 {
		t.Fatalf("issues table count = %d, want 1", tables)
	}
}

func TestApplyMigrationsRejectsInvalidDatabaseURL(t *testing.T) {
	if err := applyMigrations("postgres://track:track@localhost:notaport/track"); err == nil {
		t.Fatal("applyMigrations err = nil, want error")
	}
}
