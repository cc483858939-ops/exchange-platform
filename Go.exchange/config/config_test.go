package config

import "testing"

func TestApplySensitiveEnvironmentOverrides(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://runtime")
	t.Setenv("AI_API_KEY", "runtime-ai-key")
	t.Setenv("MINIO_ACCESS_KEY", "runtime-access")
	t.Setenv("MINIO_SECRET_KEY", "runtime-secret")

	t.Setenv("KAFKA_BROKERS", " kafka-1:9092, ,kafka-2:9092 ")
	config := &Config{}
	applySensitiveEnvironmentOverrides(config)
	if config.Database.Dsn != "postgres://runtime" {
		t.Fatalf("database dsn=%q", config.Database.Dsn)
	}
	if config.AI.APIKey != "runtime-ai-key" {
		t.Fatalf("AI key was not overridden")
	}
	if config.Storage.AccessKey != "runtime-access" || config.Storage.SecretKey != "runtime-secret" {
		t.Fatalf("storage credentials were not overridden")
	}
	if got, want := config.Kafka.Brokers, []string{"kafka-1:9092", "kafka-2:9092"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Kafka brokers=%v want=%v", got, want)
	}

}
func TestArticleViewEventsPerMinuteUsesDefaultAndClamps(t *testing.T) {
	t.Setenv("ARTICLE_VIEW_EVENTS_PER_MINUTE", "")
	if got := ArticleViewEventsPerMinute(); got != 300 {
		t.Fatalf("default limit=%d want=300", got)
	}
	t.Setenv("ARTICLE_VIEW_EVENTS_PER_MINUTE", "0")
	if got := ArticleViewEventsPerMinute(); got != 1 {
		t.Fatalf("clamped limit=%d want=1", got)
	}
	t.Setenv("ARTICLE_VIEW_EVENTS_PER_MINUTE", "450")
	if got := ArticleViewEventsPerMinute(); got != 450 {
		t.Fatalf("configured limit=%d want=450", got)
	}
}
