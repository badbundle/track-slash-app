package config

import (
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	DefaultLocalStorageRoot = "data/storage"
	LegacyLocalStorageRoot  = "tmp/storage"
	DefaultSessionTTL       = 7 * 24 * time.Hour
)

type Config struct {
	Port                 string
	DatabaseURL          string
	CORSAllowedOrigins   []string
	PublicOrigin         string
	TrustedProxyCIDRs    []net.IPNet
	SessionTTL           time.Duration
	PreviewTermsRequired bool
	DevReload            bool
	AutoMigrate          bool
	Storage              StorageConfig
	WebPush              WebPushConfig
	GitHub               GitHubConfig
}

type DatabaseConfig struct {
	DatabaseURL string
}

type StorageConfig struct {
	Backend          string
	LocalRoot        string
	Bucket           string
	MaxUploadBytes   int64
	S3Endpoint       string
	S3Region         string
	S3ForcePathStyle bool
}

type WebPushConfig struct {
	Enabled    bool
	PublicKey  string
	PrivateKey string
	Subscriber string
}

type GitHubConfig struct {
	Enabled       bool
	EncryptionKey []byte
}

func Load() (Config, error) {
	db, err := LoadDatabase()
	if err != nil {
		return Config{}, err
	}
	autoMigrate, err := envBoolOr("TRACK_SLASH_AUTO_MIGRATE", true)
	if err != nil {
		return Config{}, err
	}
	storage, err := loadStorageConfig()
	if err != nil {
		return Config{}, err
	}
	publicOrigin, err := loadPublicOrigin()
	if err != nil {
		return Config{}, err
	}
	trustedProxyCIDRs, err := loadTrustedProxyCIDRs()
	if err != nil {
		return Config{}, err
	}
	sessionTTL, err := envPositiveDuration("TRACK_SLASH_SESSION_TTL", DefaultSessionTTL)
	if err != nil {
		return Config{}, err
	}
	webPush, err := loadWebPushConfig()
	if err != nil {
		return Config{}, err
	}
	github, err := loadGitHubConfig()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Port:                 envOr("PORT", "8080"),
		DatabaseURL:          db.DatabaseURL,
		CORSAllowedOrigins:   parseList(os.Getenv("CORS_ALLOWED_ORIGINS")),
		PublicOrigin:         publicOrigin,
		TrustedProxyCIDRs:    trustedProxyCIDRs,
		SessionTTL:           sessionTTL,
		PreviewTermsRequired: envBool("TRACK_SLASH_PREVIEW_TERMS_REQUIRED"),
		DevReload:            envBool("TRACK_SLASH_DEV_RELOAD"),
		AutoMigrate:          autoMigrate,
		Storage:              storage,
		WebPush:              webPush,
		GitHub:               github,
	}
	return cfg, nil
}

func loadGitHubConfig() (GitHubConfig, error) {
	raw := strings.TrimSpace(os.Getenv("TRACK_SLASH_GITHUB_ENCRYPTION_KEY"))
	if raw == "" {
		return GitHubConfig{}, nil
	}
	key, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return GitHubConfig{}, errors.New("TRACK_SLASH_GITHUB_ENCRYPTION_KEY must be an unpadded URL-safe base64 encoding of exactly 32 bytes")
	}
	return GitHubConfig{Enabled: true, EncryptionKey: key}, nil
}

func loadWebPushConfig() (WebPushConfig, error) {
	publicKey := strings.TrimSpace(os.Getenv("TRACK_SLASH_VAPID_PUBLIC_KEY"))
	privateKey := strings.TrimSpace(os.Getenv("TRACK_SLASH_VAPID_PRIVATE_KEY"))
	subscriber := strings.TrimSpace(os.Getenv("TRACK_SLASH_VAPID_SUBSCRIBER"))
	if publicKey == "" && privateKey == "" && subscriber == "" {
		return WebPushConfig{}, nil
	}
	if publicKey == "" || privateKey == "" || subscriber == "" {
		return WebPushConfig{}, errors.New("TRACK_SLASH_VAPID_PUBLIC_KEY, TRACK_SLASH_VAPID_PRIVATE_KEY, and TRACK_SLASH_VAPID_SUBSCRIBER must be set together")
	}
	publicBytes, publicErr := base64.RawURLEncoding.DecodeString(publicKey)
	privateBytes, privateErr := base64.RawURLEncoding.DecodeString(privateKey)
	if publicErr != nil || len(publicBytes) != 65 || privateErr != nil || len(privateBytes) != 32 {
		return WebPushConfig{}, errors.New("TRACK_SLASH_VAPID_PUBLIC_KEY and TRACK_SLASH_VAPID_PRIVATE_KEY must be a valid Web Push key pair")
	}
	subscriberURL, err := url.Parse(subscriber)
	if err != nil || (subscriberURL.Scheme != "mailto" && subscriberURL.Scheme != "https") {
		return WebPushConfig{}, errors.New("TRACK_SLASH_VAPID_SUBSCRIBER must be a mailto: or https: URI")
	}
	if subscriberURL.Scheme == "mailto" && subscriberURL.Opaque == "" {
		return WebPushConfig{}, errors.New("TRACK_SLASH_VAPID_SUBSCRIBER must include a contact address")
	}
	if subscriberURL.Scheme == "https" && subscriberURL.Host == "" {
		return WebPushConfig{}, errors.New("TRACK_SLASH_VAPID_SUBSCRIBER must include a contact host")
	}
	return WebPushConfig{Enabled: true, PublicKey: publicKey, PrivateKey: privateKey, Subscriber: subscriber}, nil
}

func LoadDatabase() (DatabaseConfig, error) {
	cfg := DatabaseConfig{DatabaseURL: os.Getenv("DATABASE_URL")}
	if cfg.DatabaseURL == "" {
		return cfg, errors.New("DATABASE_URL is required")
	}
	return cfg, nil
}

// DirectConnConfig parses databaseURL for a single connection made outside
// the pgxpool, such as the realtime listener's or the migrator's. It drops
// pgxpool's pool_* settings, which size the pool, because pgx would otherwise
// send them to Postgres as runtime parameters and Postgres rejects them.
func DirectConnConfig(databaseURL string) (*pgx.ConnConfig, error) {
	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	for name := range connConfig.RuntimeParams {
		if strings.HasPrefix(name, "pool_") {
			delete(connConfig.RuntimeParams, name)
		}
	}
	return connConfig, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string) bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv(key))) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// parseList splits a comma-separated env var, trims each element, and drops
// empties. Returns nil for "" so callers can do `len() == 0` to detect off.
func parseList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func loadPublicOrigin() (string, error) {
	raw := strings.TrimSpace(os.Getenv("TRACK_SLASH_PUBLIC_ORIGIN"))
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("TRACK_SLASH_PUBLIC_ORIGIN must be an origin like https://track.example.com")
	}
	if path := strings.TrimRight(u.EscapedPath(), "/"); path != "" {
		return "", errors.New("TRACK_SLASH_PUBLIC_ORIGIN must not include a path")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && !(scheme == "http" && isLocalWebHost(u.Hostname())) {
		return "", errors.New("TRACK_SLASH_PUBLIC_ORIGIN must use https, except localhost development origins")
	}
	return (&url.URL{Scheme: scheme, Host: strings.ToLower(u.Host)}).String(), nil
}

func isLocalWebHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loadTrustedProxyCIDRs() ([]net.IPNet, error) {
	raw := parseList(os.Getenv("TRACK_SLASH_TRUSTED_PROXY_CIDRS"))
	if len(raw) == 0 {
		return nil, nil
	}
	networks := make([]net.IPNet, 0, len(raw))
	for _, candidate := range raw {
		_, network, err := net.ParseCIDR(candidate)
		if err != nil {
			return nil, errors.New("TRACK_SLASH_TRUSTED_PROXY_CIDRS must contain valid CIDR ranges")
		}
		networks = append(networks, *network)
	}
	return networks, nil
}

func loadStorageConfig() (StorageConfig, error) {
	backend := strings.ToLower(strings.TrimSpace(envOrLookup("TRACK_SLASH_STORAGE_BACKEND", "local")))
	if backend != "local" && backend != "s3" {
		return StorageConfig{}, errors.New("TRACK_SLASH_STORAGE_BACKEND must be local or s3")
	}

	localRoot := ""
	if backend == "local" {
		localRoot = strings.TrimSpace(envOrLookup("TRACK_SLASH_STORAGE_LOCAL_ROOT", DefaultLocalStorageRoot))
		if localRoot == "" {
			return StorageConfig{}, errors.New("TRACK_SLASH_STORAGE_LOCAL_ROOT is required")
		}
	}

	bucket := storageBucket(backend)
	if bucket == "" {
		return StorageConfig{}, errors.New("TRACK_SLASH_STORAGE_BUCKET is required")
	}
	maxUploadBytes, err := envPositiveInt64("TRACK_SLASH_STORAGE_MAX_UPLOAD_BYTES", 52428800)
	if err != nil {
		return StorageConfig{}, err
	}

	var s3Endpoint, s3Region string
	var s3ForcePathStyle bool
	if backend == "s3" {
		s3Endpoint = strings.TrimSpace(envOrLookup("TRACK_SLASH_STORAGE_S3_ENDPOINT", ""))
		if s3Endpoint == "" {
			return StorageConfig{}, errors.New("TRACK_SLASH_STORAGE_S3_ENDPOINT is required")
		}
		s3Region = strings.TrimSpace(envOrLookup("TRACK_SLASH_STORAGE_S3_REGION", "us-east-1"))
		if s3Region == "" {
			return StorageConfig{}, errors.New("TRACK_SLASH_STORAGE_S3_REGION is required")
		}
		s3ForcePathStyle, err = envBoolOr("TRACK_SLASH_STORAGE_S3_FORCE_PATH_STYLE", true)
		if err != nil {
			return StorageConfig{}, err
		}
	}

	return StorageConfig{
		Backend:          backend,
		LocalRoot:        localRoot,
		Bucket:           bucket,
		MaxUploadBytes:   maxUploadBytes,
		S3Endpoint:       s3Endpoint,
		S3Region:         s3Region,
		S3ForcePathStyle: s3ForcePathStyle,
	}, nil
}

func storageBucket(backend string) string {
	if raw, ok := os.LookupEnv("TRACK_SLASH_STORAGE_BUCKET"); ok {
		return strings.TrimSpace(raw)
	}
	if backend == "local" {
		return "local"
	}
	return ""
}

func envOrLookup(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envPositiveInt64(key string, fallback int64) (int64, error) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n <= 0 {
		return 0, errors.New(key + " must be a positive integer")
	}
	return n, nil
}

func envPositiveDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	duration, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || duration <= 0 {
		return 0, errors.New(key + " must be a positive duration such as 168h")
	}
	return duration, nil
}

func envBoolOr(key string, fallback bool) (bool, error) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	default:
		return false, errors.New(key + " must be a boolean")
	}
}
