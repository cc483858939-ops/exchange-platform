package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultIssuer             = "go.exchange"
	defaultAudience           = "go.exchange.api"
	defaultAccessTTL          = 15 * time.Minute
	defaultRefreshIdleTTL     = 7 * 24 * time.Hour
	defaultRefreshAbsoluteTTL = 30 * 24 * time.Hour
	defaultClockSkew          = 30 * time.Second
)

type Config struct {
	ActiveKID          string
	PrivateKey         ed25519.PrivateKey
	VerifyKeys         map[string]ed25519.PublicKey
	Issuer             string
	Audience           string
	AccessTTL          time.Duration
	RefreshIdleTTL     time.Duration
	RefreshAbsoluteTTL time.Duration
	ClockSkew          time.Duration
}

func LoadConfigFromEnv() (Config, error) {
	privateFile := strings.TrimSpace(os.Getenv("JWT_PRIVATE_KEY_FILE"))
	privateB64 := strings.TrimSpace(os.Getenv("JWT_PRIVATE_KEY_B64"))
	if (privateFile == "") == (privateB64 == "") {
		return Config{}, errors.New("exactly one of JWT_PRIVATE_KEY_FILE or JWT_PRIVATE_KEY_B64 must be set")
	}

	var privatePEM []byte
	var err error
	if privateFile != "" {
		privatePEM, err = os.ReadFile(privateFile)
		if err != nil {
			return Config{}, fmt.Errorf("read JWT private key: %w", err)
		}
	} else {
		privatePEM, err = base64.StdEncoding.DecodeString(privateB64)
		if err != nil {
			return Config{}, fmt.Errorf("decode JWT_PRIVATE_KEY_B64: %w", err)
		}
	}

	privateKey, err := parsePrivateKey(privatePEM)
	if err != nil {
		return Config{}, err
	}

	activeKID := strings.TrimSpace(os.Getenv("JWT_ACTIVE_KID"))
	if activeKID == "" {
		return Config{}, errors.New("JWT_ACTIVE_KID is required")
	}
	if strings.ContainsAny(activeKID, `/\\\x00`) {
		return Config{}, errors.New("JWT_ACTIVE_KID contains invalid characters")
	}

	verifyKeys := map[string]ed25519.PublicKey{
		activeKID: append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...),
	}
	if dir := strings.TrimSpace(os.Getenv("JWT_VERIFY_KEYS_DIR")); dir != "" {
		if err := loadVerifyKeysDir(verifyKeys, dir); err != nil {
			return Config{}, err
		}
	}
	if encoded := strings.TrimSpace(os.Getenv("JWT_VERIFY_KEYS_B64")); encoded != "" {
		if err := loadVerifyKeysB64(verifyKeys, encoded); err != nil {
			return Config{}, err
		}
	}

	accessTTL, err := envDurationStrict("JWT_ACCESS_TTL", defaultAccessTTL)
	if err != nil {
		return Config{}, err
	}
	refreshIdleTTL, err := envDurationStrict("JWT_REFRESH_IDLE_TTL", defaultRefreshIdleTTL)
	if err != nil {
		return Config{}, err
	}
	refreshAbsoluteTTL, err := envDurationStrict("JWT_REFRESH_ABSOLUTE_TTL", defaultRefreshAbsoluteTTL)
	if err != nil {
		return Config{}, err
	}
	clockSkew, err := envDurationNonNegative("JWT_CLOCK_SKEW", defaultClockSkew)
	if err != nil {
		return Config{}, err
	}
	if refreshIdleTTL > refreshAbsoluteTTL {
		return Config{}, errors.New("JWT_REFRESH_IDLE_TTL must not exceed JWT_REFRESH_ABSOLUTE_TTL")
	}

	issuer := strings.TrimSpace(os.Getenv("JWT_ISSUER"))
	if issuer == "" {
		issuer = defaultIssuer
	}
	audience := strings.TrimSpace(os.Getenv("JWT_AUDIENCE"))
	if audience == "" {
		audience = defaultAudience
	}

	return Config{
		ActiveKID:          activeKID,
		PrivateKey:         append(ed25519.PrivateKey(nil), privateKey...),
		VerifyKeys:         verifyKeys,
		Issuer:             issuer,
		Audience:           audience,
		AccessTTL:          accessTTL,
		RefreshIdleTTL:     refreshIdleTTL,
		RefreshAbsoluteTTL: refreshAbsoluteTTL,
		ClockSkew:          clockSkew,
	}, nil
}

func (c Config) validate() error {
	if c.ActiveKID == "" || len(c.PrivateKey) != ed25519.PrivateKeySize {
		return errors.New("active Ed25519 signing key is required")
	}
	publicKey, ok := c.VerifyKeys[c.ActiveKID]
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("active verification key is required")
	}
	if !bytes.Equal(publicKey, c.PrivateKey.Public().(ed25519.PublicKey)) {
		return errors.New("active verification key does not match private key")
	}
	if c.Issuer == "" || c.Audience == "" {
		return errors.New("JWT issuer and audience are required")
	}
	if c.AccessTTL <= 0 || c.RefreshIdleTTL <= 0 || c.RefreshAbsoluteTTL <= 0 {
		return errors.New("token TTL values must be positive")
	}
	if c.RefreshIdleTTL > c.RefreshAbsoluteTTL {
		return errors.New("refresh idle TTL must not exceed absolute TTL")
	}
	if c.ClockSkew < 0 {
		return errors.New("JWT clock skew must not be negative")
	}
	return nil
}

func parsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("JWT private key must contain one PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse JWT private key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("JWT private key must be an Ed25519 PKCS#8 key")
	}
	return append(ed25519.PrivateKey(nil), privateKey...), nil
}

func parsePublicKey(data []byte) (ed25519.PublicKey, error) {
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("JWT public key must contain one PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse JWT public key: %w", err)
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("JWT public key must be an Ed25519 PKIX key")
	}
	return append(ed25519.PublicKey(nil), publicKey...), nil
}

func loadVerifyKeysDir(keys map[string]ed25519.PublicKey, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read JWT verification key directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".pem") {
			continue
		}
		kid := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read JWT public key %q: %w", kid, err)
		}
		publicKey, err := parsePublicKey(data)
		if err != nil {
			return fmt.Errorf("JWT public key %q: %w", kid, err)
		}
		if err := addVerifyKey(keys, kid, publicKey); err != nil {
			return err
		}
	}
	return nil
}

func loadVerifyKeysB64(keys map[string]ed25519.PublicKey, encoded string) error {
	var values map[string]string
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return fmt.Errorf("JWT_VERIFY_KEYS_B64 must be a JSON object: %w", err)
	}
	for kid, value := range values {
		data, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return fmt.Errorf("decode JWT public key %q: %w", kid, err)
		}
		publicKey, err := parsePublicKey(data)
		if err != nil {
			return fmt.Errorf("JWT public key %q: %w", kid, err)
		}
		if err := addVerifyKey(keys, kid, publicKey); err != nil {
			return err
		}
	}
	return nil
}

func addVerifyKey(keys map[string]ed25519.PublicKey, kid string, publicKey ed25519.PublicKey) error {
	kid = strings.TrimSpace(kid)
	if kid == "" || strings.ContainsAny(kid, `/\\\x00`) {
		return errors.New("JWT verification key ID is invalid")
	}
	if existing, ok := keys[kid]; ok && !bytes.Equal(existing, publicKey) {
		return fmt.Errorf("JWT verification key %q is defined more than once", kid)
	}
	keys[kid] = append(ed25519.PublicKey(nil), publicKey...)
	return nil
}

func envDurationStrict(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}

func envDurationNonNegative(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative duration", name)
	}
	return value, nil
}
