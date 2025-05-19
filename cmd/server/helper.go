package main

import (
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

// Helper functions for environment variables and configuration

// getEnvOrDefault returns the value of an environment variable or a default if not set
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getDurationOrDefault parses a duration from an environment variable or returns the default
func getDurationOrDefault(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		} else {
			logrus.Warnf("Invalid duration in %s: %v, using default: %v", key, err, defaultValue)
		}
	}
	return defaultValue
}

// getEnvBool parses a boolean from an environment variable or returns the default
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		} else {
			logrus.Warnf("Invalid boolean in %s: %v, using default: %v", key, err, defaultValue)
		}
	}
	return defaultValue
}

// getEnvInt parses an integer from an environment variable or returns the default
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		} else {
			logrus.Warnf("Invalid integer in %s: %v, using default: %v", key, err, defaultValue)
		}
	}
	return defaultValue
}

// getEnvFloat parses a float from an environment variable or returns the default
func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return parsed
		} else {
			logrus.Warnf("Invalid float in %s: %v, using default: %v", key, err, defaultValue)
		}
	}
	return defaultValue
}
