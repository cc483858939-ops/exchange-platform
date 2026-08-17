package config

import "testing"

func TestApplySensitiveEnvironmentOverrides(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://runtime")
	t.Setenv("EMBEDDING_ENABLED", "true")
	t.Setenv("EMBEDDING_BASE_URL", "https://embedding.example")
	t.Setenv("EMBEDDING_API_KEY", "runtime-embedding-key")
	t.Setenv("EMBEDDING_MODEL", "text-embedding-3-small")
	t.Setenv("EMBEDDING_VERSION", "post_embedding_test")
	t.Setenv("EMBEDDING_TIMEOUT_SECONDS", "15")
	t.Setenv("MINIO_ACCESS_KEY", "runtime-access")
	t.Setenv("MINIO_SECRET_KEY", "runtime-secret")
	t.Setenv("KAFKA_BROKERS", " kafka-1:9092, ,kafka-2:9092 ")

	cfg := &Config{}
	applySensitiveEnvironmentOverrides(cfg)
	if cfg.Database.Dsn != "postgres://runtime" {
		t.Fatalf("database dsn=%q", cfg.Database.Dsn)
	}
	if !cfg.Embedding.Enabled || cfg.Embedding.BaseURL != "https://embedding.example" || cfg.Embedding.APIKey != "runtime-embedding-key" || cfg.Embedding.Model != "text-embedding-3-small" || cfg.Embedding.Version != "post_embedding_test" || cfg.Embedding.TimeoutSeconds != 15 {
		t.Fatalf("embedding config=%+v", cfg.Embedding)
	}
	if cfg.Storage.AccessKey != "runtime-access" || cfg.Storage.SecretKey != "runtime-secret" {
		t.Fatalf("storage credentials were not overridden")
	}
	if got, want := cfg.Kafka.Brokers, []string{"kafka-1:9092", "kafka-2:9092"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Kafka brokers=%v want=%v", got, want)
	}
}

func TestActiveEmbeddingVersionUsesConfiguredValueAndDefault(t *testing.T) {
	original := AppConfig
	t.Cleanup(func() { AppConfig = original })

	AppConfig = nil
	if got := ActiveEmbeddingVersion(); got != DefaultEmbeddingVersion {
		t.Fatalf("default version=%q want=%q", got, DefaultEmbeddingVersion)
	}
	AppConfig = &Config{Embedding: EmbeddingConfig{Version: "  post_embedding_v2  "}}
	if got := ActiveEmbeddingVersion(); got != "post_embedding_v2" {
		t.Fatalf("configured version=%q", got)
	}
	AppConfig = &Config{}
	if got := ActiveEmbeddingVersion(); got != DefaultEmbeddingVersion {
		t.Fatalf("empty version=%q want=%q", got, DefaultEmbeddingVersion)
	}
}
