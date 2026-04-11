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

	// JWT Authentication
	JWTSecret             string `mapstructure:"JWT_SECRET"`
	JWTAccessTokenExpiry  int    `mapstructure:"JWT_ACCESS_TOKEN_EXPIRY"`  // in minutes
	JWTRefreshTokenExpiry int    `mapstructure:"JWT_REFRESH_TOKEN_EXPIRY"` // in hours
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
	v.BindEnv("JWT_SECRET")
	v.BindEnv("JWT_ACCESS_TOKEN_EXPIRY")
	v.BindEnv("JWT_REFRESH_TOKEN_EXPIRY")

	// Default values
	v.SetDefault("PORT", "8080")
	v.SetDefault("ENVIRONMENT", "development")
	v.SetDefault("JWT_ACCESS_TOKEN_EXPIRY", 60)   // 1 hour
	v.SetDefault("JWT_REFRESH_TOKEN_EXPIRY", 168) // 7 days (168 hours)

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
