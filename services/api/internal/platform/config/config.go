// Package config loads and validates process configuration from the environment.
//
// Validation is strict in production: missing or weak secrets abort startup
// rather than silently falling back to insecure defaults.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Env string

const (
	EnvDevelopment Env = "development"
	EnvTest        Env = "test"
	EnvProduction  Env = "production"
)

type Config struct {
	Env      Env
	Version  string
	LogLevel string

	HTTPAddr            string
	HTTPReadTimeout     time.Duration
	HTTPWriteTimeout    time.Duration
	HTTPShutdownTimeout time.Duration
	MaxBodyBytes        int64

	DatabaseURL     string
	DatabaseMaxConn int32
	RedisURL        string

	AIServiceURL     string
	AIServiceToken   string
	AIServiceTimeout time.Duration

	SessionSecret     string
	DataEncryptionKey []byte // 32 bytes, decoded from base64

	S3Endpoint  string
	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3UseSSL    bool

	// AppBaseURL is the public origin of the web app, used in emailed links.
	AppBaseURL string
	// CookieSecure sets the Secure flag on session cookies (always true in production).
	CookieSecure bool
	// TrustProxyHeaders makes the API take the client IP from the last
	// X-Forwarded-For entry. Enable only behind a trusted reverse proxy.
	TrustProxyHeaders bool
	SessionIdleTTL    time.Duration
	SessionAbsTTL     time.Duration
	MaxUploadBytes    int64

	SMTPAddr     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
}

// StorageConfigured reports whether object storage settings are present.
func (c Config) StorageConfigured() bool {
	return c.S3Endpoint != "" && c.S3Bucket != "" && c.S3AccessKey != "" && c.S3SecretKey != ""
}

func (c Config) IsProduction() bool { return c.Env == EnvProduction }

// Load reads configuration using the provided lookup function (os.LookupEnv in
// production code; a map in tests).
func Load(lookup func(string) (string, bool)) (Config, error) {
	r := reader{lookup: lookup}
	c := Config{
		Env:      Env(r.str("APP_ENV", string(EnvDevelopment))),
		Version:  r.str("APP_VERSION", "dev"),
		LogLevel: r.str("LOG_LEVEL", "info"),

		HTTPAddr:            r.str("API_HTTP_ADDR", ":8080"),
		HTTPReadTimeout:     r.duration("API_HTTP_READ_TIMEOUT", 15*time.Second),
		HTTPWriteTimeout:    r.duration("API_HTTP_WRITE_TIMEOUT", 30*time.Second),
		HTTPShutdownTimeout: r.duration("API_HTTP_SHUTDOWN_TIMEOUT", 20*time.Second),
		MaxBodyBytes:        r.int64("API_MAX_BODY_BYTES", 1<<20),

		DatabaseURL:     r.str("DATABASE_URL", ""),
		DatabaseMaxConn: int32(r.int64("DATABASE_MAX_CONNS", 10)),
		RedisURL:        r.str("REDIS_URL", ""),

		AIServiceURL:     r.str("AI_SERVICE_URL", ""),
		AIServiceToken:   r.str("AI_SERVICE_TOKEN", ""),
		AIServiceTimeout: r.duration("AI_SERVICE_TIMEOUT", 60*time.Second),

		SessionSecret: r.str("SESSION_SECRET", ""),

		S3Endpoint:  r.str("S3_ENDPOINT", ""),
		S3Region:    r.str("S3_REGION", "us-east-1"),
		S3Bucket:    r.str("S3_BUCKET", ""),
		S3AccessKey: r.str("S3_ACCESS_KEY", ""),
		S3SecretKey: r.str("S3_SECRET_KEY", ""),
		S3UseSSL:    r.bool("S3_USE_SSL", false),

		AppBaseURL:        strings.TrimRight(r.str("APP_BASE_URL", "http://localhost:5173"), "/"),
		TrustProxyHeaders: r.bool("TRUST_PROXY_HEADERS", false),
		SessionIdleTTL:    r.duration("SESSION_IDLE_TTL", 7*24*time.Hour),
		SessionAbsTTL:     r.duration("SESSION_ABSOLUTE_TTL", 30*24*time.Hour),
		MaxUploadBytes:    r.int64("MAX_UPLOAD_BYTES", 10<<20),

		SMTPAddr:     r.str("SMTP_ADDR", ""),
		SMTPUsername: r.str("SMTP_USERNAME", ""),
		SMTPPassword: r.str("SMTP_PASSWORD", ""),
		SMTPFrom:     r.str("SMTP_FROM", ""),
	}
	c.CookieSecure = r.bool("COOKIE_SECURE", c.Env == EnvProduction)

	if raw := r.str("DATA_ENCRYPTION_KEY", ""); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			r.errs = append(r.errs, errors.New("DATA_ENCRYPTION_KEY: must be base64"))
		} else {
			c.DataEncryptionKey = key
		}
	}

	r.errs = append(r.errs, c.validate()...)
	if len(r.errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(r.errs...))
	}
	return c, nil
}

// FromEnv loads configuration from the process environment.
func FromEnv() (Config, error) { return Load(os.LookupEnv) }

func (c Config) validate() []error {
	var errs []error
	switch c.Env {
	case EnvDevelopment, EnvTest, EnvProduction:
	default:
		errs = append(errs, fmt.Errorf("APP_ENV: unsupported value %q", c.Env))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL: required"))
	} else if _, err := url.Parse(c.DatabaseURL); err != nil {
		errs = append(errs, errors.New("DATABASE_URL: not a valid URL"))
	}
	if c.DatabaseMaxConn < 1 {
		errs = append(errs, errors.New("DATABASE_MAX_CONNS: must be >= 1"))
	}
	if c.AIServiceURL != "" {
		if u, err := url.Parse(c.AIServiceURL); err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, errors.New("AI_SERVICE_URL: not a valid absolute URL"))
		}
	}
	if len(c.DataEncryptionKey) != 0 && len(c.DataEncryptionKey) != 32 {
		errs = append(errs, errors.New("DATA_ENCRYPTION_KEY: must decode to exactly 32 bytes"))
	}

	if u, err := url.Parse(c.AppBaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, errors.New("APP_BASE_URL: must be an absolute http(s) URL"))
	}
	if c.SessionIdleTTL <= 0 || c.SessionAbsTTL < c.SessionIdleTTL {
		errs = append(errs, errors.New("SESSION_IDLE_TTL / SESSION_ABSOLUTE_TTL: must be positive and absolute >= idle"))
	}
	if c.MaxUploadBytes < 1024 || c.MaxUploadBytes > 50<<20 {
		errs = append(errs, errors.New("MAX_UPLOAD_BYTES: must be between 1 KiB and 50 MiB"))
	}
	if (c.SMTPAddr == "") != (c.SMTPFrom == "") {
		errs = append(errs, errors.New("SMTP_ADDR and SMTP_FROM: set both or neither"))
	}

	if c.Env == EnvProduction {
		if !c.CookieSecure {
			errs = append(errs, errors.New("COOKIE_SECURE: cannot be disabled in production"))
		}
		if !strings.HasPrefix(c.AppBaseURL, "https://") {
			errs = append(errs, errors.New("APP_BASE_URL: must use https in production"))
		}
		if c.SMTPAddr == "" {
			errs = append(errs, errors.New("SMTP_ADDR and SMTP_FROM: required in production (password reset email)"))
		}
		if len(c.SessionSecret) < 32 {
			errs = append(errs, errors.New("SESSION_SECRET: required in production (>= 32 chars)"))
		}
		if len(c.DataEncryptionKey) != 32 {
			errs = append(errs, errors.New("DATA_ENCRYPTION_KEY: required in production"))
		}
		if c.AIServiceURL == "" || len(c.AIServiceToken) < 32 {
			errs = append(errs, errors.New("AI_SERVICE_URL and AI_SERVICE_TOKEN (>= 32 chars): required in production"))
		}
		if c.RedisURL == "" {
			errs = append(errs, errors.New("REDIS_URL: required in production"))
		}
		if !c.StorageConfigured() {
			errs = append(errs, errors.New("S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY: required in production"))
		}
	}
	return errs
}

type reader struct {
	lookup func(string) (string, bool)
	errs   []error
}

func (r *reader) str(key, def string) string {
	if v, ok := r.lookup(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func (r *reader) int64(key string, def int64) int64 {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: must be an integer", key))
		return def
	}
	return n
}

func (r *reader) bool(key string, def bool) bool {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: must be a boolean", key))
		return def
	}
	return b
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: must be a duration like 15s", key))
		return def
	}
	return d
}
