package config

import (
	"encoding/base64"
	"net"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestLoadWebPushConfig(t *testing.T) {
	publicKey := base64.RawURLEncoding.EncodeToString(make([]byte, 65))
	privateKey := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, key := range []string{"TRACK_SLASH_VAPID_PUBLIC_KEY", "TRACK_SLASH_VAPID_PRIVATE_KEY", "TRACK_SLASH_VAPID_SUBSCRIBER"} {
		unsetenv(t, key)
	}
	if got, err := loadWebPushConfig(); err != nil || got.Enabled {
		t.Fatalf("disabled Web Push = %+v, %v", got, err)
	}

	t.Setenv("TRACK_SLASH_VAPID_PUBLIC_KEY", publicKey)
	if _, err := loadWebPushConfig(); err == nil {
		t.Fatal("partial Web Push config returned nil error")
	}
	t.Setenv("TRACK_SLASH_VAPID_PRIVATE_KEY", privateKey)
	t.Setenv("TRACK_SLASH_VAPID_SUBSCRIBER", "mailto:ops@example.com")
	got, err := loadWebPushConfig()
	if err != nil || !got.Enabled || got.PublicKey != publicKey || got.PrivateKey != privateKey || got.Subscriber != "mailto:ops@example.com" {
		t.Fatalf("configured Web Push = %+v, %v", got, err)
	}

	t.Setenv("TRACK_SLASH_VAPID_SUBSCRIBER", "https://track.example.com/contact")
	if got, err := loadWebPushConfig(); err != nil || !got.Enabled {
		t.Fatalf("HTTPS subscriber Web Push = %+v, %v", got, err)
	}
	for _, subscriber := range []string{"ops@example.com", "mailto:", "https://"} {
		t.Setenv("TRACK_SLASH_VAPID_SUBSCRIBER", subscriber)
		if _, err := loadWebPushConfig(); err == nil {
			t.Fatalf("subscriber %q returned nil error", subscriber)
		}
	}
	t.Setenv("TRACK_SLASH_VAPID_SUBSCRIBER", "mailto:ops@example.com")
	t.Setenv("TRACK_SLASH_VAPID_PUBLIC_KEY", "invalid")
	if _, err := loadWebPushConfig(); err == nil {
		t.Fatal("invalid Web Push key returned nil error")
	}
}

func TestLoadGitHubConfig(t *testing.T) {
	t.Setenv("TRACK_SLASH_GITHUB_ENCRYPTION_KEY", "")
	if got, err := loadGitHubConfig(); err != nil || got.Enabled {
		t.Fatalf("disabled GitHub config = %+v, %v", got, err)
	}
	for _, raw := range []string{"invalid", base64.RawURLEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, 32))} {
		t.Setenv("TRACK_SLASH_GITHUB_ENCRYPTION_KEY", raw)
		if _, err := loadGitHubConfig(); err == nil {
			t.Fatalf("key %q returned nil error", raw)
		}
	}
	key := make([]byte, 32)
	key[0] = 7
	t.Setenv("TRACK_SLASH_GITHUB_ENCRYPTION_KEY", base64.RawURLEncoding.EncodeToString(key))
	got, err := loadGitHubConfig()
	if err != nil || !got.Enabled || !reflect.DeepEqual(got.EncryptionKey, key) {
		t.Fatalf("configured GitHub = %+v, %v", got, err)
	}
}

func TestParseList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{",,,", nil},
		{"https://a.com", []string{"https://a.com"}},
		{"https://a.com,https://b.com", []string{"https://a.com", "https://b.com"}},
		{"  https://a.com  ,  https://b.com  ", []string{"https://a.com", "https://b.com"}},
		{"a,,b", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := parseList(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("X_TEST_DEFINED", "value")
	if got := envOr("X_TEST_DEFINED", "fallback"); got != "value" {
		t.Errorf("envOr defined: got %q, want %q", got, "value")
	}
	if got := envOr("X_TEST_UNDEFINED_KEY_QWERTY", "fallback"); got != "fallback" {
		t.Errorf("envOr undefined: got %q, want %q", got, "fallback")
	}
}

func TestEnvBool(t *testing.T) {
	trueValues := []string{"1", "true", "TRUE", "t", "yes", "y", "on", " on "}
	for _, value := range trueValues {
		t.Setenv("X_TEST_BOOL", value)
		if !envBool("X_TEST_BOOL") {
			t.Fatalf("envBool(%q) = false, want true", value)
		}
	}

	falseValues := []string{"", "0", "false", "off", "no", "unexpected"}
	for _, value := range falseValues {
		t.Setenv("X_TEST_BOOL", value)
		if envBool("X_TEST_BOOL") {
			t.Fatalf("envBool(%q) = true, want false", value)
		}
	}
}

func TestLoadDatabaseOnly(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	t.Setenv("TRACK_SLASH_STORAGE_BACKEND", "s3")

	cfg, err := LoadDatabase()
	if err != nil {
		t.Fatalf("LoadDatabase: %v", err)
	}
	if cfg.DatabaseURL != "postgres://track:track@localhost:5432/track?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestLoadDatabaseRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	if _, err := LoadDatabase(); err == nil {
		t.Fatal("LoadDatabase err = nil, want error")
	}
}

func TestDirectConnConfigDropsPoolSettings(t *testing.T) {
	for _, databaseURL := range []string{
		"postgres://track:track@localhost:5432/track?sslmode=disable&pool_max_conns=20&pool_min_conns=2&search_path=app",
		"host=localhost port=5432 user=track dbname=track sslmode=disable pool_max_conns=20 pool_min_conns=2 search_path=app",
	} {
		connConfig, err := DirectConnConfig(databaseURL)
		if err != nil {
			t.Fatalf("DirectConnConfig(%q): %v", databaseURL, err)
		}
		params := connConfig.RuntimeParams
		if _, ok := params["pool_max_conns"]; ok {
			t.Fatalf("DirectConnConfig(%q) kept pool_max_conns: %v", databaseURL, params)
		}
		if _, ok := params["pool_min_conns"]; ok {
			t.Fatalf("DirectConnConfig(%q) kept pool_min_conns: %v", databaseURL, params)
		}
		if params["search_path"] != "app" {
			t.Fatalf("DirectConnConfig(%q) dropped search_path: %v", databaseURL, params)
		}
		if connConfig.Host != "localhost" || connConfig.Database != "track" {
			t.Fatalf("DirectConnConfig(%q) = host %q database %q", databaseURL, connConfig.Host, connConfig.Database)
		}
	}
}

func TestDirectConnConfigRejectsInvalidURL(t *testing.T) {
	if _, err := DirectConnConfig("postgres://track:track@localhost:notaport/track"); err == nil {
		t.Fatal("DirectConnConfig err = nil, want error")
	}
}

func TestLoadDevReload(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	t.Setenv("TRACK_SLASH_DEV_RELOAD", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.DevReload {
		t.Fatal("DevReload = false, want true")
	}
}

func TestLoadPreviewTermsRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	unsetenv(t, "TRACK_SLASH_PREVIEW_TERMS_REQUIRED")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load default preview terms: %v", err)
	}
	if cfg.PreviewTermsRequired {
		t.Fatal("default PreviewTermsRequired = true, want false")
	}

	t.Setenv("TRACK_SLASH_PREVIEW_TERMS_REQUIRED", "true")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load required preview terms: %v", err)
	}
	if !cfg.PreviewTermsRequired {
		t.Fatal("configured PreviewTermsRequired = false, want true")
	}
}

func TestLoadAutoMigrateDefault(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	t.Setenv("TRACK_SLASH_STORAGE_BACKEND", "local")
	t.Setenv("TRACK_SLASH_STORAGE_LOCAL_ROOT", "tmp/storage")
	t.Setenv("TRACK_SLASH_STORAGE_BUCKET", "local")
	unsetenv(t, "TRACK_SLASH_AUTO_MIGRATE")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.AutoMigrate {
		t.Fatal("AutoMigrate = false, want true")
	}
}

func TestLoadAutoMigrateOverride(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	t.Setenv("TRACK_SLASH_AUTO_MIGRATE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AutoMigrate {
		t.Fatal("AutoMigrate = true, want false")
	}
}

func TestLoadAutoMigrateValidation(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	t.Setenv("TRACK_SLASH_AUTO_MIGRATE", "sometimes")

	if _, err := Load(); err == nil {
		t.Fatal("Load err = nil, want error")
	}
}

func TestEnvPositiveDuration(t *testing.T) {
	const key = "X_TEST_POSITIVE_DURATION"
	unsetenv(t, key)
	if got, err := envPositiveDuration(key, time.Hour); err != nil || got != time.Hour {
		t.Fatalf("unset duration = %v, %v, want 1h", got, err)
	}

	t.Setenv(key, " 90m ")
	if got, err := envPositiveDuration(key, time.Hour); err != nil || got != 90*time.Minute {
		t.Fatalf("configured duration = %v, %v, want 90m", got, err)
	}

	for _, value := range []string{"", "not-a-duration", "0", "-1h"} {
		t.Setenv(key, value)
		if _, err := envPositiveDuration(key, time.Hour); err == nil {
			t.Fatalf("envPositiveDuration(%q) err = nil, want error", value)
		}
	}
}

func TestLoadSessionTTL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	unsetenv(t, "TRACK_SLASH_SESSION_TTL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load default session TTL: %v", err)
	}
	if cfg.SessionTTL != DefaultSessionTTL {
		t.Fatalf("default SessionTTL = %v, want %v", cfg.SessionTTL, DefaultSessionTTL)
	}

	t.Setenv("TRACK_SLASH_SESSION_TTL", "2h")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load configured session TTL: %v", err)
	}
	if cfg.SessionTTL != 2*time.Hour {
		t.Fatalf("configured SessionTTL = %v, want 2h", cfg.SessionTTL)
	}

	t.Setenv("TRACK_SLASH_SESSION_TTL", "forever")
	if _, err := Load(); err == nil {
		t.Fatal("Load invalid session TTL err = nil, want error")
	}
}

func TestLoadPublicOrigin(t *testing.T) {
	unsetenv(t, "TRACK_SLASH_PUBLIC_ORIGIN")
	got, err := loadPublicOrigin()
	if err != nil {
		t.Fatalf("loadPublicOrigin unset: %v", err)
	}
	if got != "" {
		t.Fatalf("unset public origin = %q, want empty", got)
	}

	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "https", raw: " https://Track.Example.COM ", want: "https://track.example.com"},
		{name: "https port", raw: "https://track.example.com:8443/", want: "https://track.example.com:8443"},
		{name: "localhost http", raw: "http://localhost:8080", want: "http://localhost:8080"},
		{name: "loopback http", raw: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRACK_SLASH_PUBLIC_ORIGIN", tc.raw)
			got, err := loadPublicOrigin()
			if err != nil {
				t.Fatalf("loadPublicOrigin: %v", err)
			}
			if got != tc.want {
				t.Fatalf("origin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadPublicOriginRejectsInvalidOrigins(t *testing.T) {
	for _, raw := range []string{
		"track.example.com",
		"http://track.example.com",
		"https://track.example.com/app",
		"https://track.example.com?x=1",
		"https://user@track.example.com",
		"ftp://track.example.com",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("TRACK_SLASH_PUBLIC_ORIGIN", raw)
			if _, err := loadPublicOrigin(); err == nil {
				t.Fatal("loadPublicOrigin err = nil, want error")
			}
		})
	}
}

func TestLoadTrustedProxyCIDRs(t *testing.T) {
	unsetenv(t, "TRACK_SLASH_TRUSTED_PROXY_CIDRS")
	got, err := loadTrustedProxyCIDRs()
	if err != nil || got != nil {
		t.Fatalf("unset trusted proxies = %v, %v, want nil/nil", got, err)
	}

	t.Setenv("TRACK_SLASH_TRUSTED_PROXY_CIDRS", " 192.0.2.25/24, 2001:db8:1::1/48 ")
	got, err = loadTrustedProxyCIDRs()
	if err != nil {
		t.Fatalf("loadTrustedProxyCIDRs: %v", err)
	}
	want := []string{"192.0.2.0/24", "2001:db8:1::/48"}
	if len(got) != len(want) {
		t.Fatalf("trusted proxy count = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].String() != want[i] {
			t.Fatalf("trusted proxy %d = %q, want %q", i, got[i].String(), want[i])
		}
	}

	t.Setenv("TRACK_SLASH_TRUSTED_PROXY_CIDRS", "192.0.2.1")
	if _, err := loadTrustedProxyCIDRs(); err == nil {
		t.Fatal("invalid trusted proxy err = nil, want error")
	}
}

func TestLoadIncludesTrustedProxyCIDRs(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	t.Setenv("TRACK_SLASH_TRUSTED_PROXY_CIDRS", "192.0.2.0/24")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := net.IPNet{IP: net.IPv4(192, 0, 2, 0), Mask: net.CIDRMask(24, 32)}
	if len(cfg.TrustedProxyCIDRs) != 1 || cfg.TrustedProxyCIDRs[0].String() != want.String() {
		t.Fatalf("TrustedProxyCIDRs = %v, want [%s]", cfg.TrustedProxyCIDRs, want.String())
	}
}

func TestLoadStorageDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Storage.Backend != "local" {
		t.Fatalf("Storage.Backend = %q, want local", cfg.Storage.Backend)
	}
	if cfg.Storage.LocalRoot != DefaultLocalStorageRoot {
		t.Fatalf("Storage.LocalRoot = %q, want %q", cfg.Storage.LocalRoot, DefaultLocalStorageRoot)
	}
	if cfg.Storage.Bucket != "local" {
		t.Fatalf("Storage.Bucket = %q, want local", cfg.Storage.Bucket)
	}
	if cfg.Storage.MaxUploadBytes != 52428800 {
		t.Fatalf("Storage.MaxUploadBytes = %d, want 52428800", cfg.Storage.MaxUploadBytes)
	}
	if cfg.Storage.S3Endpoint != "" || cfg.Storage.S3Region != "" || cfg.Storage.S3ForcePathStyle {
		t.Fatalf("S3 storage defaults = endpoint %q region %q force=%v, want zero values", cfg.Storage.S3Endpoint, cfg.Storage.S3Region, cfg.Storage.S3ForcePathStyle)
	}
}

func unsetenv(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%q): %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			if err := os.Setenv(key, old); err != nil {
				t.Fatalf("Setenv(%q): %v", key, err)
			}
			return
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("Unsetenv(%q): %v", key, err)
		}
	})
}

func TestLoadStorageOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	t.Setenv("TRACK_SLASH_STORAGE_BACKEND", " LOCAL ")
	t.Setenv("TRACK_SLASH_STORAGE_LOCAL_ROOT", " /var/lib/track-slash/storage ")
	t.Setenv("TRACK_SLASH_STORAGE_BUCKET", " media ")
	t.Setenv("TRACK_SLASH_STORAGE_MAX_UPLOAD_BYTES", "1234")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Storage.Backend != "local" || cfg.Storage.LocalRoot != "/var/lib/track-slash/storage" || cfg.Storage.Bucket != "media" || cfg.Storage.MaxUploadBytes != 1234 {
		t.Fatalf("Storage = %+v", cfg.Storage)
	}
}

func TestLoadS3Storage(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	t.Setenv("TRACK_SLASH_STORAGE_BACKEND", " S3 ")
	t.Setenv("TRACK_SLASH_STORAGE_BUCKET", " track-slash ")
	t.Setenv("TRACK_SLASH_STORAGE_S3_ENDPOINT", " http://garage:3900 ")
	t.Setenv("TRACK_SLASH_STORAGE_MAX_UPLOAD_BYTES", "1234")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Storage.Backend != "s3" || cfg.Storage.Bucket != "track-slash" || cfg.Storage.S3Endpoint != "http://garage:3900" || cfg.Storage.S3Region != "us-east-1" || !cfg.Storage.S3ForcePathStyle || cfg.Storage.MaxUploadBytes != 1234 {
		t.Fatalf("Storage = %+v", cfg.Storage)
	}
}

func TestLoadS3StorageOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
	t.Setenv("TRACK_SLASH_STORAGE_BACKEND", "s3")
	t.Setenv("TRACK_SLASH_STORAGE_BUCKET", "track-slash")
	t.Setenv("TRACK_SLASH_STORAGE_S3_ENDPOINT", "http://garage:3900")
	t.Setenv("TRACK_SLASH_STORAGE_S3_REGION", "eu-west-1")
	t.Setenv("TRACK_SLASH_STORAGE_S3_FORCE_PATH_STYLE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Storage.S3Region != "eu-west-1" || cfg.Storage.S3ForcePathStyle {
		t.Fatalf("S3 config = region %q force=%v, want eu-west-1/false", cfg.Storage.S3Region, cfg.Storage.S3ForcePathStyle)
	}
}

func TestLoadStorageValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		val  string
	}{
		{name: "invalid backend", key: "TRACK_SLASH_STORAGE_BACKEND", val: "gcs"},
		{name: "empty local root", key: "TRACK_SLASH_STORAGE_LOCAL_ROOT", val: ""},
		{name: "empty bucket", key: "TRACK_SLASH_STORAGE_BUCKET", val: ""},
		{name: "bad max", key: "TRACK_SLASH_STORAGE_MAX_UPLOAD_BYTES", val: "nope"},
		{name: "zero max", key: "TRACK_SLASH_STORAGE_MAX_UPLOAD_BYTES", val: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
			t.Setenv(tc.key, tc.val)
			if _, err := Load(); err == nil {
				t.Fatal("Load err = nil, want error")
			}
		})
	}
}

func TestLoadS3StorageValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{
			name: "missing bucket",
			env: map[string]string{
				"TRACK_SLASH_STORAGE_S3_ENDPOINT": "http://garage:3900",
			},
		},
		{
			name: "missing endpoint",
			env: map[string]string{
				"TRACK_SLASH_STORAGE_BUCKET": "track-slash",
			},
		},
		{
			name: "empty region",
			env: map[string]string{
				"TRACK_SLASH_STORAGE_BUCKET":      "track-slash",
				"TRACK_SLASH_STORAGE_S3_ENDPOINT": "http://garage:3900",
				"TRACK_SLASH_STORAGE_S3_REGION":   "",
			},
		},
		{
			name: "bad force path style",
			env: map[string]string{
				"TRACK_SLASH_STORAGE_BUCKET":              "track-slash",
				"TRACK_SLASH_STORAGE_S3_ENDPOINT":         "http://garage:3900",
				"TRACK_SLASH_STORAGE_S3_FORCE_PATH_STYLE": "sometimes",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://track:track@localhost:5432/track?sslmode=disable")
			t.Setenv("TRACK_SLASH_STORAGE_BACKEND", "s3")
			for key, val := range tc.env {
				t.Setenv(key, val)
			}
			if _, err := Load(); err == nil {
				t.Fatal("Load err = nil, want error")
			}
		})
	}
}
