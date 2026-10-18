package migrations_test

import (
	"database/sql"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.RunWithMigratedTemplate(m))
}

// TestUpRunsConcurrentlyAgainstOneDatabase stands in for replicas that all
// migrate at startup: each must wait for the others instead of applying the
// same migrations at once.
func TestUpRunsConcurrentlyAgainstOneDatabase(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)

	const migrators = 4
	errs := make([]error, migrators)
	var wg sync.WaitGroup
	for i := range migrators {
		sqlDB, err := sql.Open("pgx", db.URL)
		if err != nil {
			t.Fatalf("open migrator %d: %v", i, err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
		wg.Go(func() {
			errs[i] = migrations.Up(sqlDB)
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("migrator %d: %v", i, err)
		}
	}

	var applied, distinct int
	if err := db.SQL.QueryRow(`
		SELECT count(*), count(DISTINCT version_id)
		FROM goose_db_version
		WHERE version_id > 0
	`).Scan(&applied, &distinct); err != nil {
		t.Fatalf("count applied migrations: %v", err)
	}
	if applied == 0 || applied != distinct {
		t.Fatalf("goose_db_version holds %d rows for %d versions, want one row per version", applied, distinct)
	}
}
