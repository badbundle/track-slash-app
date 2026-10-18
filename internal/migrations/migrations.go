package migrations

import (
	"context"
	"database/sql"
	"embed"
	"log"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed *.sql
var FS embed.FS

// Up applies every pending migration. It holds a Postgres session advisory
// lock while it runs, so replicas that migrate at startup take turns instead
// of applying the same migrations at once.
func Up(db *sql.DB) error {
	ctx := context.Background()
	// Retry the lock every second for up to five minutes.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, FS, goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return err
	}
	for _, result := range results {
		log.Printf("goose: %s", result)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	log.Printf("goose: database at version %d", version)
	return nil
}
