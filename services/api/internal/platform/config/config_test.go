package config

import (
	"encoding/base64"
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
