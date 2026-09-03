package initialize

import (
	"errors"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestSchemaReasonCodeDoesNotExposeUnderlyingDetails(t *testing.T) {
	underlying := errors.New("dial tcp db.internal:5432: password=secret")
	wrapped := &SchemaValidationError{Code: "schema_relation_invalid"}
	if got := SchemaReasonCode(wrapped); got != "schema_relation_invalid" {
		t.Fatalf("unexpected schema reason: %q", got)
	}
	if got := SchemaReasonCode(underlying); got != "schema_check_unavailable" {
		t.Fatalf("unexpected fallback schema reason: %q", got)
	}
	if wrapped.Error() == underlying.Error() {
		t.Fatal("schema validation error unexpectedly exposed database details")
	}
}

func TestValidateResolvedSchemaRelationKindsAndCanaryErrors(t *testing.T) {
	canaries := []schemaCanary{{Table: "exchange_rates", Columns: []string{"from_currency", "to_currency"}}}
	tests := []struct {
		name    string
		rows    []schemaMetadataRow
		wantErr string
	}{
		{
			name: "complete ordinary table",
			rows: []schemaMetadataRow{
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "r", ColumnName: "from_currency"},
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "r", ColumnName: "to_currency"},
			},
		},
		{
			name: "view is invalid",
			rows: []schemaMetadataRow{
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "v", ColumnName: "from_currency"},
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "v", ColumnName: "to_currency"},
			},
			wantErr: "schema_relation_invalid",
		},
		{
			name: "materialized view is invalid",
			rows: []schemaMetadataRow{
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "m", ColumnName: "from_currency"},
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "m", ColumnName: "to_currency"},
			},
			wantErr: "schema_relation_invalid",
		},
		{
			name: "foreign table is invalid",
			rows: []schemaMetadataRow{
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "f", ColumnName: "from_currency"},
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "f", ColumnName: "to_currency"},
			},
			wantErr: "schema_relation_invalid",
		},
		{
			name: "ordinary table is allowed",
			rows: []schemaMetadataRow{
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "r", ColumnName: "from_currency"},
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "r", ColumnName: "to_currency"},
			},
		},
		{
			name: "partitioned table is allowed",
			rows: []schemaMetadataRow{
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "p", ColumnName: "from_currency"},
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "p", ColumnName: "to_currency"},
			},
		},
		{
			name:    "table is missing",
			rows:    nil,
			wantErr: "schema_table_missing",
		},
		{
			name: "column is missing",
			rows: []schemaMetadataRow{
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "r", ColumnName: "from_currency"},
			},
			wantErr: "schema_column_missing",
		},
		{
			name: "fallback columns are not merged",
			rows: []schemaMetadataRow{
				{TableName: "exchange_rates", TableSchema: "primary", RelationKind: "r", ColumnName: "from_currency"},
				{TableName: "exchange_rates", TableSchema: "fallback", RelationKind: "r", ColumnName: "to_currency"},
			},
			wantErr: "schema_column_missing",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateResolvedSchema(canaries, test.rows)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("expected schema validation success, got %v", err)
				}
				return
			}
			if got := SchemaReasonCode(err); got != test.wantErr {
				t.Fatalf("expected schema reason %q, got %q (err=%v)", test.wantErr, got, err)
			}
		})
	}
}

func TestValidateLegacySchemaMetadataRejectsLegacyContent(t *testing.T) {
	tests := []struct {
		name       string
		tableRows  []schemaMetadataRow
		columnRows []schemaMetadataRow
		wantErr    string
	}{
		{name: "clean schema"},
		{
			name:      "legacy content table",
			tableRows: []schemaMetadataRow{{TableName: "articles", TableSchema: "public", RelationKind: "r"}},
			wantErr:   "schema_legacy_content_present",
		},
		{
			name:       "legacy notification column",
			columnRows: []schemaMetadataRow{{TableName: "notifications", TableSchema: "public", RelationKind: "r", ColumnName: "article_id"}},
			wantErr:    "schema_legacy_content_present",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateLegacySchemaMetadata(test.tableRows, test.columnRows)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("expected clean legacy schema check, got %v", err)
				}
				return
			}
			if got := SchemaReasonCode(err); got != test.wantErr {
				t.Fatalf("expected schema reason %q, got %q (err=%v)", test.wantErr, got, err)
			}
		})
	}
}

func TestValidateSchemaObjectsRequiresEveryPostContractObject(t *testing.T) {
	canaries := []schemaObjectCanary{{
		Table:       "posts",
		Constraints: []string{"fk_posts_author", "chk_posts_visibility_public"},
		Indexes:     []string{"idx_posts_author_created"},
	}}
	completeConstraints := []schemaObjectMetadataRow{
		{TableName: "posts", ObjectName: "fk_posts_author"},
		{TableName: "posts", ObjectName: "chk_posts_visibility_public"},
	}
	completeIndexes := []schemaObjectMetadataRow{{TableName: "posts", ObjectName: "idx_posts_author_created"}}

	if err := validateSchemaObjects(canaries, completeConstraints, completeIndexes); err != nil {
		t.Fatalf("expected complete Post schema object check to pass: %v", err)
	}
	if got := SchemaReasonCode(validateSchemaObjects(canaries, completeConstraints[:1], completeIndexes)); got != "schema_constraint_missing" {
		t.Fatalf("expected missing constraint reason, got %q", got)
	}
	if got := SchemaReasonCode(validateSchemaObjects(canaries, completeConstraints, nil)); got != "schema_index_missing" {
		t.Fatalf("expected missing index reason, got %q", got)
	}
}

func TestRuntimeSchemaCanariesUseStableGORMRegistry(t *testing.T) {
	db, err := gorm.Open(postgres.Open("host=127.0.0.1 user=unused dbname=unused sslmode=disable"), &gorm.Config{
		DryRun:               true,
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}
	api, err := runtimeSchemaCanaries(db, false)
	if err != nil {
		t.Fatalf("build API schema registry: %v", err)
	}
	worker, err := runtimeSchemaCanaries(db, true)
	if err != nil {
		t.Fatalf("build Worker schema registry: %v", err)
	}
	apiTables := canaryTableSet(api)
	workerTables := canaryTableSet(worker)
	for _, required := range []string{
		"users", "post_media", "post_embeddings", "post_behaviors", "user_post_reco_states",
		"user_reco_profiles", "user_author_affinities", "user_reco_profile_dirty", "exchange_rates", "runtime_schema_state",
	} {
		if !apiTables[required] {
			t.Fatalf("API schema registry omitted %q", required)
		}
	}
	for _, required := range []string{"consumer_inboxes", "recommendation_daily_metrics"} {
		if !workerTables[required] {
			t.Fatalf("Worker schema registry omitted %q", required)
		}
		if apiTables[required] {
			t.Fatalf("API schema registry unexpectedly included Worker-only table %q", required)
		}
	}
	for index := 1; index < len(api); index++ {
		if api[index-1].Table >= api[index].Table {
			t.Fatalf("API schema registry is not stably sorted: %#v", api)
		}
	}
	for _, canary := range api {
		for index := 1; index < len(canary.Columns); index++ {
			if canary.Columns[index-1] >= canary.Columns[index] {
				t.Fatalf("columns for %s are not stably sorted: %#v", canary.Table, canary.Columns)
			}
		}
	}
}

func canaryTableSet(canaries []schemaCanary) map[string]bool {
	result := make(map[string]bool, len(canaries))
	for _, canary := range canaries {
		result[canary.Table] = true
	}
	return result
}

func TestPublishedSchemaVersionContractIsIndependentAndValid(t *testing.T) {
	if err := validatePublishedSchemaVersions(); err != nil {
		t.Fatalf("published schema version contract is invalid: %v", err)
	}
	if PublishedSchemaCurrentVersion != RequiredSchemaVersion || PublishedSchemaCompatibilityFloor != RequiredSchemaVersion {
		t.Fatalf("unexpected initial published schema interval: current=%d floor=%d required=%d", PublishedSchemaCurrentVersion, PublishedSchemaCompatibilityFloor, RequiredSchemaVersion)
	}
}

func TestDefaultSchemaValidationOptionsUsesBinaryVersion(t *testing.T) {
	options := DefaultSchemaValidationOptions()
	if options.RequiredVersion != RequiredSchemaVersion {
		t.Fatalf("expected required schema version %d, got %d", RequiredSchemaVersion, options.RequiredVersion)
	}
	if options.IncludeWorkerTables || options.EmbeddingEnabled {
		t.Fatalf("default API schema options unexpectedly require worker tables: %#v", options)
	}
}
