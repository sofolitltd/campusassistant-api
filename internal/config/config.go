package config

import (
	"log"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseURL string `mapstructure:"DATABASE_URL"`
	Port        string `mapstructure:"PORT"`
	Environment string `mapstructure:"ENVIRONMENT"`

	// Cloudflare R2 Storage
	R2AccessKeyID     string `mapstructure:"R2_ACCESS_KEY_ID"`
	R2SecretAccessKey string `mapstructure:"R2_SECRET_ACCESS_KEY"`
	R2BucketName      string `mapstructure:"R2_BUCKET_NAME"`
	R2AccountID       string `mapstructure:"R2_ACCOUNT_ID"`
	R2PublicURL       string `mapstructure:"R2_PUBLIC_URL"`

	// API Security
	APIKey string `mapstructure:"API_KEY"`
	// APIKeyRequired makes every /api/v1 request carry X-API-Key. Defaults to
	// true (current behaviour). The key is compiled into the mobile app, so
	// it identifies a client but cannot secure anything — set this false once
	// ACCESS_ENFORCE=true and the app no longer ships the key (the server
	// refuses to start with both off outside development). The admin
	// dashboard's server-side proxy keeps sending it either way.
	APIKeyRequired bool `mapstructure:"API_KEY_REQUIRED"`
	// CORSAllowedOrigins is a comma-separated list of browser origins allowed
	// to call the API (the Flutter web app, etc.). BKASH_CALLBACK_BASE_URL's
	// origin is always included. Empty in production means "no other origin".
	CORSAllowedOrigins string `mapstructure:"CORS_ALLOWED_ORIGINS"`
	// TrustedProxies is a comma-separated list of proxy IPs/CIDRs whose
	// X-Forwarded-For is believed (e.g. your reverse proxy). Rate limiting
	// keys on the client IP, so leaving this unset lets anyone spoof it.
	TrustedProxies string `mapstructure:"TRUSTED_PROXIES"`
	// RateLimit* — in-memory, per instance. 0 disables a limiter.
	RateLimitPerMinute     int `mapstructure:"RATE_LIMIT_PER_MINUTE"`      // all requests, per client IP
	RateLimitAuthPerMinute int `mapstructure:"RATE_LIMIT_AUTH_PER_MINUTE"` // login/register/refresh, per client IP
	RateLimitPayPerMinute  int `mapstructure:"RATE_LIMIT_PAY_PER_MINUTE"`  // payment endpoints, per user
	// AccessEnforce turns per-route access policies from log-only into
	// blocking. Defaults on in development; production sets it explicitly
	// after a log-only window has shown the classification is right.
	AccessEnforce bool `mapstructure:"ACCESS_ENFORCE"`

	// SMTP (transactional email — password reset codes). All optional:
	// if host/username/password are absent the mailer degrades to logging
	// instead of sending, exactly like FCM does without credentials.
	SMTPHost      string `mapstructure:"SMTP_HOST"`
	SMTPPort      string `mapstructure:"SMTP_PORT"`
	SMTPUsername  string `mapstructure:"SMTP_USERNAME"`
	SMTPPassword  string `mapstructure:"SMTP_PASSWORD"`
	SMTPFromEmail string `mapstructure:"SMTP_FROM_EMAIL"`
	SMTPFromName  string `mapstructure:"SMTP_FROM_NAME"`

	// Migrations
	DBAutoMigrate bool `mapstructure:"DB_AUTO_MIGRATE"`

	// JWT Authentication
	JWTSecret             string `mapstructure:"JWT_SECRET"`
	JWTAccessTokenExpiry  int    `mapstructure:"JWT_ACCESS_TOKEN_EXPIRY"`  // in minutes
	JWTRefreshTokenExpiry int    `mapstructure:"JWT_REFRESH_TOKEN_EXPIRY"` // in hours

	// Firebase Cloud Messaging — FirebaseCredentialsJSON (raw service-account JSON) takes
	// priority if set; otherwise FirebaseCredentialsFile is read from disk. Both optional —
	// push is silently disabled if neither resolves to a usable credential.
	FirebaseCredentialsJSON string `mapstructure:"FIREBASE_CREDENTIALS_JSON"`
	FirebaseCredentialsFile string `mapstructure:"FIREBASE_CREDENTIALS_FILE"`

	// bKash Tokenized Checkout — BkashProduction is the single, server-side
	// switch between sandbox and production; the client app never decides
	// this. Sandbox defaults to bKash's shared public sandbox credentials.
	BkashProduction       bool   `mapstructure:"BKASH_PRODUCTION"`
	BkashSandboxUsername  string `mapstructure:"BKASH_SANDBOX_USERNAME"`
	BkashSandboxPassword  string `mapstructure:"BKASH_SANDBOX_PASSWORD"`
	BkashSandboxAppKey    string `mapstructure:"BKASH_SANDBOX_APP_KEY"`
	BkashSandboxAppSecret string `mapstructure:"BKASH_SANDBOX_APP_SECRET"`
	BkashProdUsername     string `mapstructure:"BKASH_PROD_USERNAME"`
	BkashProdPassword     string `mapstructure:"BKASH_PROD_PASSWORD"`
	BkashProdAppKey       string `mapstructure:"BKASH_PROD_APP_KEY"`
	BkashProdAppSecret    string `mapstructure:"BKASH_PROD_APP_SECRET"`
	// BkashCallbackBaseURL is where bKash redirects the user's browser/WebView
	// after payment — the Flutter web app's origin (its /payment route).
	BkashCallbackBaseURL string `mapstructure:"BKASH_CALLBACK_BASE_URL"`
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigFile(".env")
	v.AutomaticEnv()

	// Explicitly bind environment variables
	v.BindEnv("DATABASE_URL")
	v.BindEnv("PORT")
	v.BindEnv("ENVIRONMENT")
	v.BindEnv("R2_ACCESS_KEY_ID")
	v.BindEnv("R2_SECRET_ACCESS_KEY")
	v.BindEnv("R2_BUCKET_NAME")
	v.BindEnv("R2_ACCOUNT_ID")
	v.BindEnv("R2_PUBLIC_URL")
	v.BindEnv("API_KEY")
	v.BindEnv("ACCESS_ENFORCE")
	v.BindEnv("API_KEY_REQUIRED")
	v.BindEnv("CORS_ALLOWED_ORIGINS")
	v.BindEnv("TRUSTED_PROXIES")
	v.BindEnv("RATE_LIMIT_PER_MINUTE")
	v.BindEnv("RATE_LIMIT_AUTH_PER_MINUTE")
	v.BindEnv("RATE_LIMIT_PAY_PER_MINUTE")
	v.BindEnv("SMTP_HOST")
	v.BindEnv("SMTP_PORT")
	v.BindEnv("SMTP_USERNAME")
	v.BindEnv("SMTP_PASSWORD")
	v.BindEnv("SMTP_FROM_EMAIL")
	v.BindEnv("SMTP_FROM_NAME")
	v.BindEnv("JWT_SECRET")
	v.BindEnv("JWT_ACCESS_TOKEN_EXPIRY")
	v.BindEnv("JWT_REFRESH_TOKEN_EXPIRY")
	v.BindEnv("DB_AUTO_MIGRATE")
	v.BindEnv("FIREBASE_CREDENTIALS_JSON")
	v.BindEnv("FIREBASE_CREDENTIALS_FILE")
	v.BindEnv("BKASH_PRODUCTION")
	v.BindEnv("BKASH_SANDBOX_USERNAME")
	v.BindEnv("BKASH_SANDBOX_PASSWORD")
	v.BindEnv("BKASH_SANDBOX_APP_KEY")
	v.BindEnv("BKASH_SANDBOX_APP_SECRET")
	v.BindEnv("BKASH_PROD_USERNAME")
	v.BindEnv("BKASH_PROD_PASSWORD")
	v.BindEnv("BKASH_PROD_APP_KEY")
	v.BindEnv("BKASH_PROD_APP_SECRET")
	v.BindEnv("BKASH_CALLBACK_BASE_URL")

	// Default values
	v.SetDefault("PORT", "8080")
	v.SetDefault("ENVIRONMENT", "development")
	v.SetDefault("ACCESS_ENFORCE", false)
	v.SetDefault("API_KEY_REQUIRED", true)
	v.SetDefault("RATE_LIMIT_PER_MINUTE", 600)
	v.SetDefault("RATE_LIMIT_AUTH_PER_MINUTE", 30)
	v.SetDefault("RATE_LIMIT_PAY_PER_MINUTE", 30)
	v.SetDefault("JWT_ACCESS_TOKEN_EXPIRY", 60)   // 1 hour
	v.SetDefault("JWT_REFRESH_TOKEN_EXPIRY", 168) // 7 days (168 hours)
	v.SetDefault("FIREBASE_CREDENTIALS_FILE", "./firebase-service-account.json")
	v.SetDefault("BKASH_PRODUCTION", false)
	v.SetDefault("BKASH_SANDBOX_USERNAME", "sandboxTokenizedUser02")
	v.SetDefault("BKASH_SANDBOX_PASSWORD", "sandboxTokenizedUser02@12345")
	v.SetDefault("BKASH_SANDBOX_APP_KEY", "4f6o0cjiki2rfm34kfdadl1eqq")
	v.SetDefault("BKASH_SANDBOX_APP_SECRET", "2is7hdktrekvrbljjh44ll3d9l1dtjo4pasmjvs5vl5qr3fug4b")
	v.SetDefault("BKASH_CALLBACK_BASE_URL", "https://campusassistant.web.app")

	if err := v.ReadInConfig(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, err
	}

	// Trim whitespace/newlines from DATABASE_URL to avoid parsing errors in production
	config.DatabaseURL = strings.TrimSpace(config.DatabaseURL)

	return &config, nil
}

// SplitList splits a comma-separated env value, dropping blanks.
func SplitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
