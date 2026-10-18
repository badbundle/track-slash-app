package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/bradleymackey/track-slash/internal/config"
	"github.com/bradleymackey/track-slash/internal/githubintegration"
	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/notifications"
	"github.com/bradleymackey/track-slash/internal/realtime"
	"github.com/bradleymackey/track-slash/internal/server"
	objectstorage "github.com/bradleymackey/track-slash/internal/storage"
	"github.com/bradleymackey/track-slash/internal/store"
)

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "run migrations and exit")
	createAdmin := flag.Bool("create-admin-token", false, "create or update an admin user, create an API token, print it, and exit")
	adminEmail := flag.String("email", "", "email for -create-admin-token")
	adminName := flag.String("name", "", "name for -create-admin-token")
	adminTokenName := flag.String("token-name", "bootstrap", "token name for -create-admin-token")
	promoteExisting := flag.Bool("promote-existing", false, "with -create-admin-token, make the existing non-admin account that holds -email an admin (emails are not verified, so check who holds it first)")
	generateVAPIDKeys := flag.Bool("generate-vapid-keys", false, "generate a Web Push VAPID key pair and exit")
	healthcheck := flag.Bool("healthcheck", false, "check the health of the server listening on PORT, exit 0 if healthy and 1 otherwise")
	flag.Parse()
	if *healthcheck {
		if err := checkHealth("http://127.0.0.1:" + config.LoadPort()); err != nil {
			log.Fatalf("healthcheck: %v", err)
		}
		return
	}
	if *generateVAPIDKeys {
		privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			log.Fatalf("generate VAPID keys: %v", err)
		}
		fmt.Printf("TRACK_SLASH_VAPID_PUBLIC_KEY=%s\n", publicKey)
		fmt.Printf("TRACK_SLASH_VAPID_PRIVATE_KEY=%s\n", privateKey)
		return
	}

	if *migrateOnly {
		db, err := config.LoadDatabase()
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		if err := applyMigrations(db.DatabaseURL); err != nil {
			log.Fatalf("migrations: %v", err)
		}
		log.Println("migrations applied; exiting (migrate-only)")
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if cfg.AutoMigrate {
		if err := applyMigrations(cfg.DatabaseURL); err != nil {
			log.Fatalf("migrations: %v", err)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("pg ping: %v", err)
	}

	st := store.New(pool)
	if *createAdmin {
		if err := createAdminToken(ctx, st, *adminEmail, *adminName, *adminTokenName, *promoteExisting); err != nil {
			log.Fatalf("create admin token: %v", err)
		}
		return
	}

	if cfg.PublicOrigin == "" {
		log.Println("warning: TRACK_SLASH_PUBLIC_ORIGIN is unset; behind a TLS-terminating proxy, cookies are set without Secure and UI changes sent through htmx fail the CSRF origin check. Set it outside local development.")
	}

	hub := realtime.NewHub()
	listener := realtime.NewListener(cfg.DatabaseURL, hub)
	go listener.Run(ctx)

	if err := migrateLegacyLocalStorage(cfg.Storage); err != nil {
		log.Fatalf("storage: %v", err)
	}
	storageSvc, err := newObjectStorage(ctx, cfg.Storage)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	deletionWorker := objectstorage.NewDeletionWorker(st, storageSvc, objectstorage.DeletionWorkerOptions{})
	go deletionWorker.Run(ctx)
	if cfg.WebPush.Enabled {
		pushSender, err := notifications.NewWebPushSender(
			cfg.WebPush.PublicKey,
			cfg.WebPush.PrivateKey,
			cfg.WebPush.Subscriber,
			nil,
		)
		if err != nil {
			log.Fatalf("web push: %v", err)
		}
		pushWorker := notifications.NewWorker(st, pushSender, notifications.WorkerOptions{})
		go pushWorker.Run(ctx)
	}
	var githubService *githubintegration.Service
	if cfg.GitHub.Enabled {
		cryptor, err := githubintegration.NewCryptor(cfg.GitHub.EncryptionKey)
		if err != nil {
			log.Fatalf("github integration: %v", err)
		}
		githubService = githubintegration.NewService(st, githubintegration.NewClient(nil, ""), cryptor, githubintegration.ServiceOptions{})
		githubWorker := githubintegration.NewWorker(st, githubService, githubintegration.WorkerOptions{})
		go githubWorker.Run(ctx)
	}
	emailMailer, err := newMailer(cfg.Email)
	if err != nil {
		log.Fatalf("email: %v", err)
	}

	srv := server.NewWithOptions(st, hub, server.Options{
		CORSAllowedOrigins:   cfg.CORSAllowedOrigins,
		PublicOrigin:         cfg.PublicOrigin,
		TrustedProxyCIDRs:    cfg.TrustedProxyCIDRs,
		SessionTTL:           cfg.SessionTTL,
		PreviewTermsRequired: cfg.PreviewTermsRequired,
		DevReload:            cfg.DevReload,
		ObjectStorage:        storageSvc,
		WebPushPublicKey:     cfg.WebPush.PublicKey,
		GitHubIntegration:    githubService,
		Mailer:               emailMailer,
	})

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on :%s", cfg.Port)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Println("shutdown signal received")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	log.Println("bye")
}

func createAdminToken(ctx context.Context, st *store.Store, email, name, tokenName string, promoteExisting bool) error {
	email = strings.TrimSpace(email)
	name = strings.TrimSpace(name)
	tokenName = strings.TrimSpace(tokenName)
	if email == "" || !strings.Contains(email, "@") {
		return errors.New("valid -email is required with -create-admin-token")
	}
	if name == "" {
		return errors.New("-name is required with -create-admin-token")
	}
	if tokenName == "" {
		return errors.New("-token-name is required with -create-admin-token")
	}
	u, err := st.CreateOrUpdateAdminUser(ctx, email, name)
	if errors.Is(err, store.ErrEmailBelongsToDeletedUser) {
		return fmt.Errorf("%w; rerun with a different -email to create a new admin", err)
	}
	if errors.Is(err, store.ErrEmailBelongsToNonAdmin) {
		if !promoteExisting {
			return fmt.Errorf("%w; emails are not verified, so check who holds it, then rerun with -promote-existing to make that account an admin", err)
		}
		u, err = st.PromoteUserToAdminByEmail(ctx, email)
	}
	if err != nil {
		return err
	}
	created, err := st.CreateAuthToken(ctx, store.CreateAuthTokenParams{
		UserID: u.ID,
		Kind:   model.AuthTokenKindAPI,
		Name:   tokenName,
	})
	if err != nil {
		return err
	}
	fmt.Printf("user_id=%s\n", u.ID)
	fmt.Printf("token_id=%s\n", created.Token.ID)
	fmt.Printf("token=%s\n", created.RawToken)
	return nil
}

func migrateLegacyLocalStorage(cfg config.StorageConfig) error {
	if cfg.Backend != "local" || cfg.LocalRoot != config.DefaultLocalStorageRoot {
		return nil
	}

	legacyRoot := config.LegacyLocalStorageRoot
	legacyInfo, err := os.Stat(legacyRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy local storage root %q: %w", legacyRoot, err)
	}
	if !legacyInfo.IsDir() {
		return fmt.Errorf("legacy local storage root %q is not a directory", legacyRoot)
	}
	if _, err := os.Stat(cfg.LocalRoot); err == nil {
		return fmt.Errorf("cannot relocate legacy local storage from %q to %q: destination already exists; move the files manually", legacyRoot, cfg.LocalRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect local storage root %q: %w", cfg.LocalRoot, err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LocalRoot), 0700); err != nil {
		return fmt.Errorf("create local storage parent directory: %w", err)
	}
	if err := os.Rename(legacyRoot, cfg.LocalRoot); err != nil {
		return fmt.Errorf("relocate legacy local storage from %q to %q: %w", legacyRoot, cfg.LocalRoot, err)
	}
	return nil
}

func newObjectStorage(ctx context.Context, cfg config.StorageConfig) (*objectstorage.Service, error) {
	switch cfg.Backend {
	case "local":
		return objectstorage.NewLocalService(cfg.LocalRoot, cfg.Bucket, cfg.MaxUploadBytes)
	case "s3":
		return objectstorage.NewS3Service(ctx, cfg.Bucket, cfg.MaxUploadBytes, objectstorage.S3Config{
			Endpoint:       cfg.S3Endpoint,
			Region:         cfg.S3Region,
			ForcePathStyle: cfg.S3ForcePathStyle,
		})
	default:
		return nil, fmt.Errorf("unsupported storage backend %q", cfg.Backend)
	}
}

func applyMigrations(dbURL string) error {
	connConfig, err := config.DirectConnConfig(dbURL)
	if err != nil {
		return err
	}
	db := stdlib.OpenDB(*connConfig)
	defer db.Close()
	return migrations.Up(db)
}
