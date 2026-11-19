package config

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadDevelopmentDefaults(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://u:p@localhost:5432/db"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Env != EnvDevelopment || c.HTTPAddr != ":8080" || c.DatabaseMaxConn != 10 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoadRejectsMalformedValues(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{
		"DATABASE_URL":          "postgres://localhost/db",
		"API_HTTP_READ_TIMEOUT": "soon",
		"DATABASE_MAX_CONNS":    "many",
		"S3_USE_SSL":            "maybe",
		"APP_ENV":               "staging",
		"AI_SERVICE_URL":        "not a url",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"API_HTTP_READ_TIMEOUT", "DATABASE_MAX_CONNS", "S3_USE_SSL", "APP_ENV", "AI_SERVICE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %s: %v", want, err)
		}
	}
}

func TestProductionRequiresSecrets(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{
		"APP_ENV":      "production",
		"DATABASE_URL": "postgres://localhost/db",
	}))
	if err == nil {
		t.Fatal("expected production validation to fail without secrets")
	}
	for _, want := range []string{"SESSION_SECRET", "DATA_ENCRYPTION_KEY", "AI_SERVICE_TOKEN", "REDIS_URL", "S3_BUCKET", "SMTP_ADDR", "APP_BASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %s: %v", want, err)
		}
	}
}

func TestProductionAcceptsCompleteConfig(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	_, err := Load(lookupFrom(map[string]string{
		"APP_ENV":             "production",
		"DATABASE_URL":        "postgres://localhost/db",
		"REDIS_URL":           "redis://localhost:6379/0",
		"SESSION_SECRET":      strings.Repeat("s", 32),
		"DATA_ENCRYPTION_KEY": key,
		"AI_SERVICE_URL":      "http://ai:8090",
		"AI_SERVICE_TOKEN":    strings.Repeat("t", 32),
		"S3_ENDPOINT":         "s3:8333",
		"S3_BUCKET":           "b", "S3_ACCESS_KEY": "a", "S3_SECRET_KEY": "s",
		"APP_BASE_URL": "https://app.example.com",
		"SMTP_ADDR":    "smtp.example.com:587", "SMTP_FROM": "noreply@example.com",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEncryptionKeyMustBe32Bytes(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{
		"DATABASE_URL":        "postgres://localhost/db",
		"DATA_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString([]byte("short")),
	}))
	if err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Fatalf("expected key length error, got %v", err)
	}
}

func TestSecretsCanComeFromFiles(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/session"
	if err := os.WriteFile(path, []byte("  a-session-secret-from-a-mounted-file-xyz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://localhost/db", "SESSION_SECRET_FILE": path}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.SessionSecret != "a-session-secret-from-a-mounted-file-xyz" {
		t.Fatalf("secret not read from file (got %d chars)", len(c.SessionSecret))
	}

	// Both set is ambiguous and rejected; a missing file is an error that
	// names the path but never contents.
	_, err = Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://localhost/db", "SESSION_SECRET": "x", "SESSION_SECRET_FILE": path}))
	if err == nil || !strings.Contains(err.Error(), "set only one") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
	_, err = Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://localhost/db", "SESSION_SECRET_FILE": dir + "/missing"}))
	if err == nil || !strings.Contains(err.Error(), "SESSION_SECRET_FILE: cannot read") {
		t.Fatalf("expected read error, got %v", err)
	}
}

func TestRateLimitAndDrainBounds(t *testing.T) {
	c, err := Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://localhost/db"}))
	if err != nil || c.RateLimitPerMinute != 300 || c.HTTPShutdownDrain != 0 || c.MetricsAddr != "" {
		t.Fatalf("unexpected defaults: %+v %v", c, err)
	}
	_, err = Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://localhost/db", "API_RATE_LIMIT_PER_MINUTE": "-1", "API_HTTP_SHUTDOWN_DRAIN": "5m"}))
	if err == nil || !strings.Contains(err.Error(), "API_RATE_LIMIT_PER_MINUTE") || !strings.Contains(err.Error(), "API_HTTP_SHUTDOWN_DRAIN") {
		t.Fatalf("expected bound errors, got %v", err)
	}
}

func TestRetiredEncryptionKeys(t *testing.T) {
	k := func(b byte) string {
		return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(rune('a'+b)), 32)))
	}
	c, err := Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://localhost/db", "DATA_ENCRYPTION_KEY": k(2),
		"DATA_ENCRYPTION_KEY_VERSION": "2", "DATA_ENCRYPTION_KEYS_RETIRED": "1:" + k(1)}))
	if err != nil {
		t.Fatal(err)
	}
	ring, err := c.Keyring()
	if err != nil || ring.Current() != 2 || len(c.RetiredEncryptionKeys) != 1 {
		t.Fatalf("keyring: %v %+v", err, c.RetiredEncryptionKeys)
	}
	for _, bad := range []string{"2:" + k(1), "x:" + k(1), "1:short", "1:" + k(1) + ",1:" + k(3)} {
		_, err := Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://localhost/db", "DATA_ENCRYPTION_KEY": k(2),
			"DATA_ENCRYPTION_KEY_VERSION": "2", "DATA_ENCRYPTION_KEYS_RETIRED": bad}))
		if err == nil || !strings.Contains(err.Error(), "DATA_ENCRYPTION_KEYS_RETIRED") {
			t.Fatalf("%q accepted: %v", bad, err)
		}
	}
}

func TestDatabaseOnlyConfigForMigrations(t *testing.T) {
	c, err := LoadDatabaseOnly(lookupFrom(map[string]string{"APP_ENV": "production", "DATABASE_URL": "postgres://localhost/db"}))
	if err != nil || c.DatabaseURL == "" {
		t.Fatalf("migrator config: %v", err)
	}
	if _, err := LoadDatabaseOnly(lookupFrom(map[string]string{"APP_ENV": "production"})); err == nil {
		t.Fatal("missing DATABASE_URL accepted")
	}
}
