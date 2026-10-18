package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/bradleymackey/track-slash/internal/config"
	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/seed"
	"github.com/bradleymackey/track-slash/internal/store"
)

func main() {
	username := flag.String("username", envOr("SEED_USERNAME", "demo"), "username to create or reuse")
	password := flag.String("password", envOr("SEED_PASSWORD", "correct-horse-battery"), "password for the seeded user")
	name := flag.String("name", envOr("SEED_NAME", "Demo User"), "display name for the seeded user")
	projectPrefix := flag.String("project-prefix", envOr("SEED_PROJECT_PREFIX", "DEMO"), "1-6 char project key prefix")
	applyMigrations := flag.Bool("migrate", envBool("SEED_MIGRATE", true), "apply migrations before seeding")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	if *applyMigrations {
		if err := migrate(cfg.DatabaseURL); err != nil {
			log.Fatalf("migrations: %v", err)
		}
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("pg ping: %v", err)
	}

	summary, err := seed.Run(ctx, store.New(pool), seed.Options{
		Username:      *username,
		Password:      *password,
		Name:          *name,
		ProjectPrefix: *projectPrefix,
	})
	if err != nil {
		log.Fatalf("seed: %v", err)
	}

	printSummary(summary)
}

func migrate(dbURL string) error {
	connConfig, err := config.DirectConnConfig(dbURL)
	if err != nil {
		return err
	}
	db := stdlib.OpenDB(*connConfig)
	defer db.Close()
	return migrations.Up(db)
}

func printSummary(summary seed.Summary) {
	userState := "reused"
	if summary.CreatedUser {
		userState = "created"
	}
	fmt.Printf("user %s (%s) %s\n", summary.User.Username, summary.User.ID, userState)
	for _, project := range summary.Projects {
		projectState := "reused"
		if project.CreatedProject {
			projectState = "created"
		}
		contentState := "seeded"
		if project.ExistingContent {
			contentState = "skipped existing content"
		}
		fmt.Printf(
			"project %s %s: %s, %d sprints, %d issues, %d sub-issues, %d comments, %d links\n",
			project.Project.Key,
			projectState,
			contentState,
			project.SprintsCreated,
			project.IssuesCreated,
			project.SubIssuesCreated,
			project.CommentsCreated,
			project.LinksCreated,
		)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch value {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
