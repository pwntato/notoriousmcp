package config

import (
	"log"
	"os"
	"strconv"
)

// Int64EnvOrDefault reads an environment variable as int64. Returns def if the
// variable is unset or unparseable (logs a warning on parse failure).
func Int64EnvOrDefault(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.Printf("warning: %s=%q is not a valid int64, using default %d", key, v, def)
		return def
	}
	return n
}
