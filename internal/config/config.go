package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr              string
	HTTPReadTimeout       time.Duration
	HTTPReadHeaderTimeout time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPIdleTimeout       time.Duration
	LogLevel              string
	ShutdownTimeout       time.Duration

	DatabaseURL             string
	DatabaseMaxConns        int32
	DatabaseMinConns        int32
	DatabaseMaxConnLifetime time.Duration
	DatabaseConnectTimeout  time.Duration
	DatabaseQueryTimeout    time.Duration
}

func Load() (Config, error) {
	var cfg Config
	var err error

	if cfg.HTTPAddr, err = required("HTTP_ADDR"); err != nil {
		return Config{}, err
	}
	if cfg.HTTPReadTimeout, err = duration("HTTP_READ_TIMEOUT"); err != nil {
		return Config{}, err
	}
	if cfg.HTTPReadHeaderTimeout, err = duration("HTTP_READ_HEADER_TIMEOUT"); err != nil {
		return Config{}, err
	}
	if cfg.HTTPWriteTimeout, err = duration("HTTP_WRITE_TIMEOUT"); err != nil {
		return Config{}, err
	}
	if cfg.HTTPIdleTimeout, err = duration("HTTP_IDLE_TIMEOUT"); err != nil {
		return Config{}, err
	}
	if cfg.LogLevel, err = required("LOG_LEVEL"); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = duration("SHUTDOWN_TIMEOUT"); err != nil {
		return Config{}, err
	}
	if cfg.DatabaseURL, err = required("DATABASE_URL"); err != nil {
		return Config{}, err
	}
	if cfg.DatabaseMaxConns, err = positiveInt32("DATABASE_MAX_CONNS"); err != nil {
		return Config{}, err
	}
	if cfg.DatabaseMinConns, err = nonNegativeInt32("DATABASE_MIN_CONNS"); err != nil {
		return Config{}, err
	}
	if cfg.DatabaseMaxConnLifetime, err = duration("DATABASE_MAX_CONN_LIFETIME"); err != nil {
		return Config{}, err
	}
	if cfg.DatabaseConnectTimeout, err = duration("DATABASE_CONNECT_TIMEOUT"); err != nil {
		return Config{}, err
	}
	if cfg.DatabaseQueryTimeout, err = duration("DATABASE_QUERY_TIMEOUT"); err != nil {
		return Config{}, err
	}

	if cfg.DatabaseMinConns > cfg.DatabaseMaxConns {
		return Config{}, fmt.Errorf(
			"DATABASE_MIN_CONNS (%d) must not exceed DATABASE_MAX_CONNS (%d)",
			cfg.DatabaseMinConns,
			cfg.DatabaseMaxConns,
		)
	}

	return cfg, nil
}

func required(name string) (string, error) {
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return "", fmt.Errorf("%s is required", name)
	}

	return value, nil
}

func duration(name string) (time.Duration, error) {
	value, err := required(name)
	if err != nil {
		return 0, err
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}

	return parsed, nil
}

func positiveInt32(name string) (int32, error) {
	value, err := nonNegativeInt32(name)
	if err != nil {
		return 0, err
	}
	if value == 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}

	return value, nil
}

func nonNegativeInt32(name string) (int32, error) {
	value, err := required(name)
	if err != nil {
		return 0, err
	}

	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be a 32-bit integer: %w", name, err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%s must not be negative", name)
	}

	return int32(parsed), nil
}
