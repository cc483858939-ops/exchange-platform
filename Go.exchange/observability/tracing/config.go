package tracing

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultServiceName   = "exchange-api"
	defaultOTLPEndpoint  = "tempo:4317"
	defaultSampleRatio   = 0.05
	defaultExportTimeout = 3 * time.Second
	defaultShutdownTime  = 5 * time.Second
)

// Config contains the API process OpenTelemetry trace settings.
type Config struct {
	Enabled         bool
	ServiceName     string
	OTLPEndpoint    string
	SampleRatio     float64
	ExportTimeout   time.Duration
	ShutdownTimeout time.Duration
}

// LoadConfig reads tracing settings without changing the existing application config.
func LoadConfig() (Config, error) {
	cfg := Config{
		ServiceName:     defaultServiceName,
		OTLPEndpoint:    defaultOTLPEndpoint,
		SampleRatio:     defaultSampleRatio,
		ExportTimeout:   defaultExportTimeout,
		ShutdownTimeout: defaultShutdownTime,
	}

	if raw, ok := os.LookupEnv("TRACING_ENABLED"); ok {
		enabled, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return Config{}, fmt.Errorf("TRACING_ENABLED must be a boolean: %w", err)
		}
		cfg.Enabled = enabled
	}
	if value, ok := os.LookupEnv("TRACING_SERVICE_NAME"); ok {
		cfg.ServiceName = strings.TrimSpace(value)
	}
	if value, ok := os.LookupEnv("TRACING_OTLP_ENDPOINT"); ok {
		cfg.OTLPEndpoint = strings.TrimSpace(value)
	}
	if raw, ok := os.LookupEnv("TRACING_SAMPLE_RATIO"); ok {
		ratio, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return Config{}, fmt.Errorf("TRACING_SAMPLE_RATIO must be a number between 0 and 1: %w", err)
		}
		cfg.SampleRatio = ratio
	}
	if raw, ok := os.LookupEnv("TRACING_EXPORT_TIMEOUT"); ok {
		timeout, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return Config{}, fmt.Errorf("TRACING_EXPORT_TIMEOUT must be a valid duration: %w", err)
		}
		cfg.ExportTimeout = timeout
	}
	if raw, ok := os.LookupEnv("TRACING_SHUTDOWN_TIMEOUT"); ok {
		timeout, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return Config{}, fmt.Errorf("TRACING_SHUTDOWN_TIMEOUT must be a valid duration: %w", err)
		}
		cfg.ShutdownTimeout = timeout
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.ServiceName) == "" {
		return fmt.Errorf("TRACING_SERVICE_NAME must not be empty")
	}
	if !(cfg.SampleRatio >= 0 && cfg.SampleRatio <= 1) {
		return fmt.Errorf("TRACING_SAMPLE_RATIO must be between 0 and 1, got %v", cfg.SampleRatio)
	}
	if cfg.ExportTimeout <= 0 {
		return fmt.Errorf("TRACING_EXPORT_TIMEOUT must be greater than zero")
	}
	if cfg.ShutdownTimeout <= 0 {
		return fmt.Errorf("TRACING_SHUTDOWN_TIMEOUT must be greater than zero")
	}
	if _, _, err := parseEndpoint(cfg.OTLPEndpoint); err != nil {
		return fmt.Errorf("TRACING_OTLP_ENDPOINT is invalid: %w", err)
	}
	return nil
}

// parseEndpoint returns the gRPC target and whether TLS is enabled. Plain host:port
// targets use the local OTLP gRPC transport; https:// targets enable TLS.
func parseEndpoint(endpoint string) (string, bool, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", false, fmt.Errorf("endpoint must not be empty")
	}

	secure := false
	target := endpoint
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return "", false, fmt.Errorf("expected host:port or https://host:port")
		}
		if parsed.Scheme != "https" || parsed.User != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", false, fmt.Errorf("expected host:port or https://host:port")
		}
		target = parsed.Host
		secure = true
	}

	host, port, err := net.SplitHostPort(target)
	if err != nil || strings.TrimSpace(host) == "" || port == "" {
		return "", false, fmt.Errorf("expected host:port or https://host:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", false, fmt.Errorf("port must be between 1 and 65535")
	}
	return target, secure, nil
}
