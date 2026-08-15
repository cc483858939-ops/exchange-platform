package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func privateKeyPEM(t *testing.T) []byte {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
}

func clearJWTEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"JWT_ACTIVE_KID",
		"JWT_PRIVATE_KEY_FILE",
		"JWT_PRIVATE_KEY_B64",
		"JWT_VERIFY_KEYS_DIR",
		"JWT_VERIFY_KEYS_B64",
		"JWT_ISSUER",
		"JWT_AUDIENCE",
		"JWT_ACCESS_TTL",
		"JWT_REFRESH_IDLE_TTL",
		"JWT_REFRESH_ABSOLUTE_TTL",
		"JWT_CLOCK_SKEW",
	} {
		t.Setenv(name, "")
	}
}

func TestLoadConfigFromEnvRequiresExactlyOnePrivateKeySource(t *testing.T) {
	clearJWTEnvironment(t)
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("missing private key must fail")
	}
	t.Setenv("JWT_PRIVATE_KEY_FILE", "private.pem")
	t.Setenv("JWT_PRIVATE_KEY_B64", "also-set")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("multiple private key sources must fail")
	}
}

func TestLoadConfigFromEnvAcceptsPKCS8Base64AndDerivesActivePublicKey(t *testing.T) {
	clearJWTEnvironment(t)
	t.Setenv("JWT_ACTIVE_KID", "test-v1")
	t.Setenv("JWT_PRIVATE_KEY_B64", base64.StdEncoding.EncodeToString(privateKeyPEM(t)))
	config, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.PrivateKey) != ed25519.PrivateKeySize {
		t.Fatalf("private key length=%d", len(config.PrivateKey))
	}
	if len(config.VerifyKeys[config.ActiveKID]) != ed25519.PublicKeySize {
		t.Fatal("active public key was not derived")
	}
	if config.AccessTTL != defaultAccessTTL || config.Issuer != defaultIssuer || config.Audience != defaultAudience {
		t.Fatalf("unexpected defaults: %+v", config)
	}
}

func TestLoadConfigFromEnvAcceptsPrivateKeyFileAndRejectsInvalidTTL(t *testing.T) {
	clearJWTEnvironment(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "private.pem")
	if err := os.WriteFile(path, privateKeyPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JWT_ACTIVE_KID", "test-v1")
	t.Setenv("JWT_PRIVATE_KEY_FILE", path)
	t.Setenv("JWT_ACCESS_TTL", "not-a-duration")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("invalid access TTL must fail")
	}
}
