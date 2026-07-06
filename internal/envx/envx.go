// Package envx provides typed environment-variable helpers with sensible
// defaults and warning-on-parse-error behaviour. It consolidates the env
// helpers that were previously duplicated between internal/config and
// cmd/server.
package envx

import (
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

// String returns the value of the environment variable named by key, or
// defaultValue if the variable is empty or unset.
func String(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return defaultValue
}

// Int returns the value of the environment variable named by key as an int,
// or defaultValue if the variable is unset or cannot be parsed.
func Int(key string, defaultValue int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		logrus.Warnf("invalid int for %s=%q, using default %d", key, v, defaultValue)
	}
	return defaultValue
}

// Int64 returns the value of the environment variable named by key as an
// int64, or defaultValue if the variable is unset or cannot be parsed.
func Int64(key string, defaultValue int64) int64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
		logrus.Warnf("invalid int64 for %s=%q, using default %d", key, v, defaultValue)
	}
	return defaultValue
}

// Float64 returns the value of the environment variable named by key as a
// float64, or defaultValue if the variable is unset or cannot be parsed.
func Float64(key string, defaultValue float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
		logrus.Warnf("invalid float for %s=%q, using default %f", key, v, defaultValue)
	}
	return defaultValue
}

// Duration returns the value of the environment variable named by key as a
// time.Duration, or defaultValue if the variable is unset or cannot be parsed.
func Duration(key string, defaultValue time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		logrus.Warnf("invalid duration for %s=%q, using default %s", key, v, defaultValue)
	}
	return defaultValue
}

// Bool returns the value of the environment variable named by key as a bool,
// or defaultValue if the variable is unset or cannot be parsed.
func Bool(key string, defaultValue bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
		logrus.Warnf("invalid bool for %s=%q, using default %t", key, v, defaultValue)
	}
	return defaultValue
}
