package cdc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

// SourceDatabaseConfig separates the authoritative database identity from the
// credentials used by the Debezium connector.
type SourceDatabaseConfig struct {
	Host     string
	Port     uint16
	Database string

	User     string
	Password string

	SSLMode     string
	SSLCert     string
	SSLKey      string
	SSLRootCert string
}

type EnvironmentConfig struct {
	ConnectURL  string
	User        string
	Password    string
	SSLMode     string
	SSLCert     string
	SSLKey      string
	SSLRootCert string
}

func Environment() EnvironmentConfig {
	connectURL := strings.TrimSpace(os.Getenv("CDC_CONNECT_URL"))
	if connectURL == "" {
		connectURL = ConnectURL
	}
	return EnvironmentConfig{
		ConnectURL:  connectURL,
		User:        strings.TrimSpace(os.Getenv("CDC_DATABASE_USER")),
		Password:    os.Getenv("CDC_DATABASE_PASSWORD"),
		SSLMode:     strings.TrimSpace(os.Getenv("CDC_DATABASE_SSLMODE")),
		SSLCert:     strings.TrimSpace(os.Getenv("CDC_DATABASE_SSLCERT")),
		SSLKey:      strings.TrimSpace(os.Getenv("CDC_DATABASE_SSLKEY")),
		SSLRootCert: strings.TrimSpace(os.Getenv("CDC_DATABASE_SSLROOTCERT")),
	}
}

func ParseSourceDatabaseConfig(dsn string, environment EnvironmentConfig) (SourceDatabaseConfig, error) {
	if strings.TrimSpace(dsn) == "" {
		return SourceDatabaseConfig{}, errors.New("DATABASE_DSN is not configured")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		return SourceDatabaseConfig{}, fmt.Errorf("parse DATABASE_DSN: %w", err)
	}
	source := SourceDatabaseConfig{
		Host: parsed.Host, Port: parsed.Port, Database: parsed.Database,
		User: parsed.User, Password: parsed.Password,
		SSLMode: environment.SSLMode, SSLCert: environment.SSLCert,
		SSLKey: environment.SSLKey, SSLRootCert: environment.SSLRootCert,
	}
	if strings.TrimSpace(environment.User) == "" {
		source.User = parsed.User
	} else {
		source.User = strings.TrimSpace(environment.User)
	}
	if environment.Password != "" {
		source.Password = environment.Password
	}
	if source.SSLMode == "" {
		if parsed.TLSConfig == nil {
			source.SSLMode = "disable"
		} else {
			source.SSLMode = "prefer"
		}
	}
	if err := ValidateSourceDatabaseConfig(source); err != nil {
		return SourceDatabaseConfig{}, err
	}
	return source, nil
}

func ValidateSourceDatabaseConfig(source SourceDatabaseConfig) error {
	if strings.TrimSpace(source.Host) == "" {
		return errors.New("CDC source database host is empty")
	}
	if source.Port == 0 {
		return errors.New("CDC source database port is empty")
	}
	if strings.TrimSpace(source.Database) == "" {
		return errors.New("CDC source database name is empty")
	}
	if strings.TrimSpace(source.User) == "" {
		return errors.New("CDC source database user is empty")
	}
	allowedSSLMode := map[string]struct{}{
		"disable": {}, "allow": {}, "prefer": {}, "require": {},
		"verify-ca": {}, "verify-full": {},
	}
	if _, ok := allowedSSLMode[strings.ToLower(strings.TrimSpace(source.SSLMode))]; !ok {
		return fmt.Errorf("invalid CDC database sslmode %q", source.SSLMode)
	}
	return nil
}

func ValidateCurrentDatabase(ctx context.Context, db *sql.DB, source SourceDatabaseConfig) error {
	if db == nil {
		return errors.New("database connection is nil")
	}
	var current string
	if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&current); err != nil {
		return fmt.Errorf("read current database identity: %w", err)
	}
	if current != source.Database {
		return fmt.Errorf("connected database %q does not match DATABASE_DSN database %q", current, source.Database)
	}
	return nil
}
