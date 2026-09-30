// Package config loads the KaziWise runtime configuration from the
// process environment (optionally seeded from a .env file).
//
// Every value the backend needs is declared in .env.example. The loader is
// strict: anything missing that the server genuinely cannot run without
// produces an actionable error rather than a nil-pointer panic later.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type AuthMode string

const (
	// AuthSupabase verifies Supabase-issued JWTs. Users live in
	// Supabase Auth; roles and departments live in `profiles`.
	AuthSupabase AuthMode = "supabase"
	// AuthLocal is a prototype escape hatch: credentials are verified
	// against Supabase Auth when possible, and a signed local JWT is
	// issued instead. Never enable in production.
	AuthLocal AuthMode = "local"
)

type StorageDriver string

const (
	StorageSupabase StorageDriver = "supabase"
	StorageLocal    StorageDriver = "local"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	Env     string // development | staging | production
	AppName string
	BaseURL string // public URL of THIS api, used to build share links

	HTTPAddr string
	HTTP     HTTPConfig

	DatabaseURL     string
	DBMaxConns      int32
	DBMinConns      int32
	DBConnectRetry  time.Duration
	DBStatementTO   time.Duration
	DBQueryMode     string
	MigrationDir    string
	RunMigrations   bool
	SeedDemo        bool
	SeedAuthOnStart bool

	AuthMode        AuthMode
	SupabaseURL     string
	SupabaseAnonKey string
	SupabaseService string
	SupabaseJWTSec  string
	SupabaseJWKSURL string
	JWTSigningAlg   string
	Issuer          string
	Audience        string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	AllowLocalAuth  bool

	Storage       StorageDriver
	StorageBucket MaterialsBucket
	LocalDiskRoot string
	MaxUploadMB   int64
	PresignTTL    time.Duration

	CORSAllowedOrigins []string
	TrustedProxies     []string
	RateLimitPerMin    int
	RequestTimeout     time.Duration
	CookieDomain       string
	CookieSecure       bool
	LogLevel           string

	DownloadURLTTL time.Duration

	// Demo bootstrap account used by `-seed-demo` / the /setup endpoint.
	SeedOrgName  string
	SeedOrgSlug  string
	SeedPassword string
}

// MaterialsBucket names the Supabase Storage buckets the LMS writes to.
type MaterialsBucket struct {
	Course string // pdf / pptx / images / video course material
	Avatar string // employee avatars
	Temp   string // scratch space for in-flight uploads
}

type HTTPConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownGrace     time.Duration
}

// Load reads .env (if present) and then the process environment.
// Precedence: real environment variables always win over .env values.
func Load() (*Config, error) {
	// .env is optional; a missing file is not an error.
	for _, candidate := range []string{".env", filepath.Join("config", ".env")} {
		if _, err := os.Stat(candidate); err == nil {
			_ = godotenv.Load(candidate)
			break
		}
	}

	cfg := &Config{
		Env:      env("APP_ENV", "development"),
		AppName:  env("APP_NAME", "KaziWise LMS API"),
		BaseURL:  strings.TrimRight(env("APP_BASE_URL", "http://localhost:8080"), "/"),
		HTTPAddr: env("HTTP_ADDR", ":8080"),
		DatabaseURL: firstNonEmpty(
			os.Getenv("DATABASE_URL"),
			os.Getenv("SUPABASE_DB_URL"),
			os.Getenv("POSTGRES_URL"),
		),

		AuthMode:        AuthMode(env("AUTH_MODE", string(AuthSupabase))),
		SupabaseURL:     strings.TrimRight(os.Getenv("SUPABASE_URL"), "/"),
		SupabaseAnonKey: os.Getenv("SUPABASE_ANON_KEY"),
		SupabaseService: os.Getenv("SUPABASE_SERVICE_ROLE_KEY"),
		SupabaseJWTSec:  firstNonEmpty(os.Getenv("SUPABASE_JWT_SECRET"), os.Getenv("SUPABASE_JWT_SECRET_KEY")),
		JWTSigningAlg:   env("SUPABASE_JWT_ALG", "auto"),
		Issuer:          os.Getenv("JWT_ISSUER"),
		Audience:        os.Getenv("JWT_AUDIENCE"),
		AccessTokenTTL:  envDuration("ACCESS_TOKEN_TTL", 60*time.Minute),
		RefreshTokenTTL: envDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		AllowLocalAuth:  envBool("ALLOW_LOCAL_AUTH", false),

		Storage:       StorageDriver(env("STORAGE_DRIVER", string(StorageSupabase))),
		LocalDiskRoot: env("LOCAL_DISK_ROOT", "./.kaziwise-data"),
		MaxUploadMB:   int64(envInt("MAX_UPLOAD_MB", 512)),
		PresignTTL:    envDuration("PRESIGNED_URL_TTL", 15*time.Minute),

		// A lesson can run far longer than a single API call, and the player
		// hands this URL straight to pdf.js. A 15 minute link expires
		// mid-lesson and the viewer dies on a long document, so the
		// default is sized to comfortably outlast one sitting.
		DownloadURLTTL: envDuration("DOWNLOAD_URL_TTL", 6*time.Hour),

		SeedOrgName:  env("SEED_ORG_NAME", "KaziWise Demo Ltd"),
		SeedOrgSlug:  env("SEED_ORG_SLUG", "kaziwise-demo"),
		SeedPassword: env("SEED_PASSWORD", "Password123!"),
	}

	cfg.StorageBucket = MaterialsBucket{
		Course: env("STORAGE_BUCKET_COURSE", "course-materials"),
		Avatar: env("STORAGE_BUCKET_AVATAR", "avatars"),
		Temp:   env("STORAGE_BUCKET_TEMP", "uploads"),
	}

	cfg.HTTP = HTTPConfig{
		ReadHeaderTimeout: envDuration("HTTP_READ_HEADER_TIMEOUT", 10*time.Second),
		ReadTimeout:       envDuration("HTTP_READ_TIMEOUT", 5*time.Minute), // large uploads
		WriteTimeout:      envDuration("HTTP_WRITE_TIMEOUT", 5*time.Minute),
		IdleTimeout:       envDuration("HTTP_IDLE_TIMEOUT", 2*time.Minute),
		ShutdownGrace:     envDuration("SHUTDOWN_GRACE", 15*time.Second),
	}
	cfg.DBMaxConns = int32(envInt("DB_MAX_CONNS", 20))
	cfg.DBMinConns = int32(envInt("DB_MIN_CONNS", 2))
	cfg.DBConnectRetry = envDuration("DB_CONNECT_RETRY", 30*time.Second)
	cfg.DBStatementTO = envDuration("DB_STATEMENT_TIMEOUT", 15*time.Second)
	cfg.DBQueryMode = env("DB_QUERY_MODE", "exec")
	cfg.MigrationDir = env("MIGRATION_DIR", "./migrations")
	cfg.RunMigrations = envBool("RUN_MIGRATIONS", true)
	cfg.SeedDemo = envBool("SEED_DEMO", false)
	cfg.SeedAuthOnStart = envBool("SEED_AUTH", false)

	cfg.CORSAllowedOrigins = envList("CORS_ALLOWED_ORIGINS", []string{"http://localhost:3000", "http://localhost:5173", "http://127.0.0.1:5173"})
	cfg.TrustedProxies = envList("TRUSTED_PROXIES", nil)
	cfg.RateLimitPerMin = envInt("RATE_LIMIT_PER_MIN", 300)
	cfg.RequestTimeout = envDuration("REQUEST_TIMEOUT", 0)
	cfg.CookieDomain = os.Getenv("COOKIE_DOMAIN")
	cfg.CookieSecure = envBool("COOKIE_SECURE", cfg.Env == "production")
	cfg.LogLevel = env("LOG_LEVEL", "info")

	cfg.SupabaseJWKSURL = firstNonEmpty(
		os.Getenv("SUPABASE_JWKS_URL"),
		joinURL(cfg.SupabaseURL, "/auth/v1/.well-known/jwks.json"),
	)

	if cfg.Env == "production" {
		if !strings.HasPrefix(cfg.BaseURL, "https://") {
			return nil, errors.New("APP_BASE_URL must start with https:// in production")
		}
		if cfg.AllowLocalAuth {
			return nil, errors.New("ALLOW_LOCAL_AUTH must be false in production")
		}
		if cfg.Storage == StorageLocal {
			return nil, errors.New("STORAGE_DRIVER must not be 'local' in production")
		}
	}
	return cfg, cfg.Validate()
}

// Validate reports configuration that would make the server unusable.
func (c *Config) Validate() error {
	var problems []string

	if c.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is required (Supabase: Project Settings -> Database -> Connection string, use the *pooler* URI)")
	}
	switch c.DBQueryMode {
	case "cache_statement", "cache_describe", "describe_exec", "exec", "simple_protocol":
	default:
		problems = append(problems, fmt.Sprintf(
			"DB_QUERY_MODE must be one of cache_statement, cache_describe, describe_exec, exec, simple_protocol; got %q",
			c.DBQueryMode))
	}
	switch c.AuthMode {
	case AuthSupabase, AuthLocal:
	default:
		problems = append(problems, fmt.Sprintf("AUTH_MODE must be %q or %q, got %q", AuthSupabase, AuthLocal, c.AuthMode))
	}
	if c.AuthMode == AuthSupabase || c.AllowLocalAuth || c.SeedAuthOnStart {
		if c.SupabaseURL == "" {
			problems = append(problems, "SUPABASE_URL is required")
		}
		if c.SupabaseService == "" {
			problems = append(problems, "SUPABASE_SERVICE_ROLE_KEY is required for user provisioning and storage uploads")
		}
	}
	if c.SupabaseJWTSec == "" && c.JWTSigningAlg == "HS256" {
		problems = append(problems, "SUPABASE_JWT_SECRET is required when SUPABASE_JWT_ALG=HS256")
	}
	switch c.Storage {
	case StorageSupabase:
		if c.SupabaseService == "" || c.SupabaseURL == "" {
			problems = append(problems, "STORAGE_DRIVER=supabase requires SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY")
		}
	case StorageLocal:
	default:
		problems = append(problems, fmt.Sprintf("STORAGE_DRIVER must be %q or %q, got %q", StorageSupabase, StorageLocal, c.Storage))
	}
	if c.MaxUploadMB <= 0 {
		problems = append(problems, "MAX_UPLOAD_MB must be greater than zero")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// Redacted returns the configuration safe to print at boot.
func (c *Config) Redacted() map[string]any {
	return map[string]any{
		"app.env":            c.Env,
		"app.name":           c.AppName,
		"app.base_url":       c.BaseURL,
		"http.addr":          c.HTTPAddr,
		"auth.mode":          string(c.AuthMode),
		"supabase.url":       c.SupabaseURL,
		"storage.driver":     string(c.Storage),
		"storage.bucket":     c.StorageBucket.Course,
		"db.url":             redactDSN(c.DatabaseURL),
		"cors.origins":       c.CORSAllowedOrigins,
		"max_upload_mb":      c.MaxUploadMB,
		"seed_demo":          c.SeedDemo,
		"request_timeout":    c.RequestTimeout.String(),
		"rate_limit_per_min": c.RateLimitPerMin,
	}
}

func redactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	if i := strings.Index(dsn, "@"); i > 0 {
		scheme := ""
		if j := strings.Index(dsn, "://"); j >= 0 {
			scheme = dsn[:j+3]
		}
		return scheme + "***:***" + dsn[i:]
	}
	return "***"
}

func joinURL(base, path string) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + path
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func envList(key string, fallback []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
