package config

import (
	"log"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseURL string `mapstructure:"DATABASE_URL"`
	Port        string `mapstructure:"PORT"`
	Environment string `mapstructure:"ENVIRONMENT"`
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigFile(".env")
	v.AutomaticEnv()

	// Explicitly bind environment variables
	v.BindEnv("DATABASE_URL")
	v.BindEnv("PORT")
	v.BindEnv("ENVIRONMENT")

	// Default values
	v.SetDefault("PORT", "8080")
	v.SetDefault("ENVIRONMENT", "development")

	if err := v.ReadInConfig(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, err
	}
	return &config, nil
}
