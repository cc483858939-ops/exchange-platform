package config

import (
	"os"
	"strconv"
	"strings"
)

const (
	RuntimeRoleAll    = "all"
	RuntimeRoleAPI    = "api"
	RuntimeRoleWorker = "worker"
)

func RuntimeRole() string {
	role := strings.ToLower(strings.TrimSpace(os.Getenv("APP_RUNTIME_ROLE")))
	switch role {
	case "", RuntimeRoleAll:
		return RuntimeRoleAll
	case RuntimeRoleAPI:
		return RuntimeRoleAPI
	case RuntimeRoleWorker:
		return RuntimeRoleWorker
	default:
		return RuntimeRoleAll
	}
}

func AppPort() string {
	port := strings.TrimSpace(os.Getenv("APP_PORT"))
	if port == "" {
		port = strings.TrimSpace(os.Getenv("PORT"))
	}
	if port == "" && AppConfig != nil {
		port = strings.TrimSpace(AppConfig.App.Port)
	}
	if port == "" {
		port = "3000"
	}
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}
	return port
}

func DatabaseDSN() string {
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_DSN")); dsn != "" {
		return dsn
	}
	if AppConfig != nil {
		return AppConfig.Database.Dsn
	}
	return ""
}

func RedisAddr() string {
	if addr := strings.TrimSpace(os.Getenv("REDIS_ADDR")); addr != "" {
		return addr
	}
	return "redis:6379"
}

func RedisPassword() string {
	return strings.TrimSpace(os.Getenv("REDIS_PASSWORD"))
}

func RedisDB() int {
	return envInt("REDIS_DB", 0)
}

func RedisPoolSize() int {
	return envInt("REDIS_POOL_SIZE", 1000)
}

func RedisMinIdleConns() int {
	return envInt("REDIS_MIN_IDLE_CONNS", 50)
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}
