package initialize

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"Go.exchange/models"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RequiredSchemaVersion is the schema version required by this binary.
const RequiredSchemaVersion int64 = 3

// PublishedSchemaCurrentVersion and PublishedSchemaCompatibilityFloor are
// migration-owned values. They are deliberately separate from the binary's
// required version so a migration can publish a compatibility interval that
// spans more than one release.
const (
	PublishedSchemaCurrentVersion     int64 = 3
	PublishedSchemaCompatibilityFloor int64 = 3
)

const runtimeSchemaStateID uint = 1

type SchemaValidationOptions struct {
	RequiredVersion     int64
	IncludeWorkerTables bool
	// EmbeddingEnabled is retained for caller compatibility. PostEmbedding
	// is part of the API schema contract regardless of runtime feature flags.
	EmbeddingEnabled bool
}

type SchemaValidationError struct {
	Code string
}

func (e *SchemaValidationError) Error() string {
	if e == nil || e.Code == "" {
		return "runtime schema validation failed"
	}
	return e.Code
}

func schemaError(code string) error { return &SchemaValidationError{Code: code} }

func SchemaReasonCode(err error) string {
	var validationErr *SchemaValidationError
	if errors.As(err, &validationErr) && validationErr.Code != "" {
		return validationErr.Code
	}
	return "schema_check_unavailable"
}

type schemaCanary struct {
	Table   string
	Columns []string
}

type schemaObjectCanary struct {
	Table       string
	Constraints []string
	Indexes     []string
}

type legacySchemaColumn struct {
	Table  string
	Column string
}

var legacyContentTableNames = []string{
	"articles",
	"post_articles",
	"comments",
	"article_reaction",
	"article_reposts",
	"article_embeddings",
	"article_behaviors",
	"user_article_reco_states",
}

var legacyRuntimeColumns = []legacySchemaColumn{
	{Table: "notifications", Column: "article_id"},
	{Table: "notifications", Column: "comment_id"},
	{Table: "recommendation_result_traces", Column: "article_id"},
	{Table: "recommendation_daily_metrics", Column: "article_id"},
}

var postSchemaObjectCanaries = []schemaObjectCanary{
	{
		Table: "posts",
		Constraints: []string{
			"fk_posts_author",
			"fk_posts_reply_to_post",
			"fk_posts_quote_post",
			"fk_posts_conversation",
			"chk_posts_visibility_public",
			"chk_posts_reply_quote_exclusive",
			"chk_posts_conversation_shape",
			"chk_posts_like_count_nonnegative",
			"chk_posts_reply_count_nonnegative",
			"chk_posts_view_count_nonnegative",
			"chk_posts_like_sync_version_nonnegative",
		},
		Indexes: []string{
			"idx_posts_author_created",
			"idx_posts_reply_to_created",
			"idx_posts_conversation_created",
			"idx_posts_quote",
			"idx_posts_deleted_at",
		},
	},
	{
		Table: "post_reaction",
		Constraints: []string{
			"fk_post_reaction_user",
			"fk_post_reaction_post",
		},
	},
	{
		Table: "post_reposts",
		Constraints: []string{
			"fk_post_reposts_user",
			"fk_post_reposts_post",
		},
		Indexes: []string{
			"uidx_post_reposts_user_post",
			"idx_post_reposts_user_created",
			"idx_post_reposts_post",
		},
	},
	{
		Table: "post_embeddings",
		Constraints: []string{
			"fk_post_embeddings_post",
			"chk_post_embeddings_vector_dimensions",
		},
	},
	{
		Table: "post_behaviors",
		Constraints: []string{
			"fk_post_behaviors_user",
			"fk_post_behaviors_post",
		},
	},
	{
		Table: "user_post_reco_states",
		Constraints: []string{
			"fk_user_post_reco_states_user",
			"fk_user_post_reco_states_post",
		},
	},
}

var apiSchemaModels = []interface{}{
	&models.User{},
	&models.UserFollow{},
	&models.Post{},
	&models.PostRepost{},
	&models.PostReaction{},
	&models.Notification{},
	&models.RecommendationRequest{},
	&models.RecommendationResultTrace{},
	&models.OutboxEvent{},
	&models.ExchangeRate{},
	&models.PostEmbedding{},
	&models.PostBehavior{},
	&models.UserPostRecoState{},
	&models.UserRecoProfile{},
	&models.UserAuthorAffinity{},
	&models.UserRecoProfileDirty{},
	&models.RuntimeSchemaState{},
}

var workerSchemaModels = []interface{}{
	&models.ConsumerInbox{},
	&models.RecommendationDailyMetric{},
}

type schemaMetadataRow struct {
	TableName    string `gorm:"column:table_name"`
	TableSchema  string `gorm:"column:table_schema"`
	RelationKind string `gorm:"column:relation_kind"`
	ColumnName   string `gorm:"column:column_name"`
}

type schemaObjectMetadataRow struct {
	TableName   string `gorm:"column:table_name"`
	TableSchema string `gorm:"column:table_schema"`
	ObjectName  string `gorm:"column:object_name"`
}

// runtimeSchemaCanaries derives the contract from GORM metadata. This keeps
// TableName overrides and every persisted field in sync with the models.
func runtimeSchemaCanaries(db *gorm.DB, includeWorkerTables bool) ([]schemaCanary, error) {
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	modelsToInspect := append([]interface{}(nil), apiSchemaModels...)
	if includeWorkerTables {
		modelsToInspect = append(modelsToInspect, workerSchemaModels...)
	}
	byTable := make(map[string]map[string]struct{}, len(modelsToInspect))
	for _, model := range modelsToInspect {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil || statement.Schema == nil || statement.Schema.Table == "" {
			return nil, errors.New("unable to parse runtime schema model")
		}
		columns := byTable[statement.Schema.Table]
		if columns == nil {
			columns = make(map[string]struct{})
			byTable[statement.Schema.Table] = columns
		}
		for _, field := range statement.Schema.Fields {
			if field.DBName != "" {
				columns[field.DBName] = struct{}{}
			}
		}
	}

	result := make([]schemaCanary, 0, len(byTable))
	for table, columnSet := range byTable {
		columns := make([]string, 0, len(columnSet))
		for column := range columnSet {
			columns = append(columns, column)
		}
		sort.Strings(columns)
		result = append(result, schemaCanary{Table: table, Columns: columns})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Table < result[j].Table })
	return result, nil
}

func DefaultSchemaValidationOptions() SchemaValidationOptions {
	return SchemaValidationOptions{RequiredVersion: RequiredSchemaVersion}
}

// CheckRuntimeSchema performs the lightweight, read-only runtime contract
// check. It intentionally returns only stable reason codes to callers.
func CheckRuntimeSchema(ctx context.Context, db *gorm.DB, options SchemaValidationOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return schemaError("schema_database_unavailable")
	}
	if err := ctx.Err(); err != nil {
		return schemaError("schema_check_timeout")
	}
	if options.RequiredVersion <= 0 {
		options.RequiredVersion = RequiredSchemaVersion
	}
	return validateRuntimeSchema(ctx, db.WithContext(ctx), options)
}

func validateRuntimeSchema(ctx context.Context, db *gorm.DB, options SchemaValidationOptions) error {
	var state models.RuntimeSchemaState
	if err := db.Table("runtime_schema_state").Where("id = ?", runtimeSchemaStateID).Take(&state).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return schemaError("schema_state_missing")
		}
		if isUndefinedTableError(err) {
			return schemaError("schema_state_missing")
		}
		if isSchemaCheckTimeout(ctx, err) {
			return schemaError("schema_check_timeout")
		}
		return schemaError("schema_state_unavailable")
	}
	if state.CurrentVersion < RequiredSchemaVersion || state.CompatibilityFloor < RequiredSchemaVersion ||
		state.CompatibilityFloor > state.CurrentVersion ||
		state.CompatibilityFloor > options.RequiredVersion || options.RequiredVersion > state.CurrentVersion {
		return schemaError("schema_incompatible")
	}

	return validateSchemaCanaries(ctx, db, options)
}

func validateSchemaCanaries(ctx context.Context, db *gorm.DB, options SchemaValidationOptions) error {
	if err := ctx.Err(); err != nil {
		return schemaError("schema_check_timeout")
	}
	if err := validateLegacySchemaAbsence(ctx, db); err != nil {
		return err
	}
	canaries, err := runtimeSchemaCanaries(db, options.IncludeWorkerTables)
	if err != nil {
		return schemaError("schema_check_unavailable")
	}
	tables := make([]string, 0, len(canaries))
	for _, canary := range canaries {
		tables = append(tables, canary.Table)
	}
	var rows []schemaMetadataRow
	if err := db.WithContext(ctx).Raw(`
WITH search_path AS (
    SELECT schema_name, ordinality
    FROM unnest(current_schemas(false))
         WITH ORDINALITY AS path(schema_name, ordinality)
),
resolved_relations AS (
    SELECT DISTINCT ON (class.relname)
           class.relname AS table_name,
           namespace.nspname AS table_schema,
           class.oid AS relation_oid,
           class.relkind::text AS relation_kind,
           search_path.ordinality
    FROM search_path
    JOIN pg_namespace AS namespace
      ON namespace.nspname = search_path.schema_name
    JOIN pg_class AS class
      ON class.relnamespace = namespace.oid
    WHERE class.relname IN ?
    ORDER BY class.relname, search_path.ordinality
)
SELECT resolved_relations.table_name,
       resolved_relations.table_schema,
       resolved_relations.relation_kind,
       attribute.attname AS column_name
FROM resolved_relations
LEFT JOIN pg_attribute AS attribute
  ON attribute.attrelid = resolved_relations.relation_oid
 AND attribute.attnum > 0
 AND NOT attribute.attisdropped
ORDER BY resolved_relations.table_name, attribute.attnum
`, tables).Scan(&rows).Error; err != nil {
		if isSchemaCheckTimeout(ctx, err) {
			return schemaError("schema_check_timeout")
		}
		return schemaError("schema_check_unavailable")
	}
	if err := validateResolvedSchema(canaries, rows); err != nil {
		return err
	}
	return validatePostSchemaObjects(ctx, db)
}

func validatePostSchemaObjects(ctx context.Context, db *gorm.DB) error {
	if err := ctx.Err(); err != nil {
		return schemaError("schema_check_timeout")
	}
	if len(postSchemaObjectCanaries) == 0 {
		return nil
	}
	tables := make([]string, 0, len(postSchemaObjectCanaries))
	for _, canary := range postSchemaObjectCanaries {
		tables = append(tables, canary.Table)
	}
	const resolvedTables = `
WITH search_path AS (
    SELECT schema_name, ordinality
    FROM unnest(current_schemas(false))
         WITH ORDINALITY AS path(schema_name, ordinality)
),
resolved_relations AS (
    SELECT DISTINCT ON (class.relname)
           class.relname AS table_name,
           namespace.nspname AS table_schema,
           class.oid AS relation_oid,
           search_path.ordinality
    FROM search_path
    JOIN pg_namespace AS namespace
      ON namespace.nspname = search_path.schema_name
    JOIN pg_class AS class
      ON class.relnamespace = namespace.oid
    WHERE class.relname IN ?
    ORDER BY class.relname, search_path.ordinality
)
`

	var constraintRows []schemaObjectMetadataRow
	if err := db.WithContext(ctx).Raw(resolvedTables+`
SELECT resolved_relations.table_name,
       resolved_relations.table_schema,
       constraints_catalog.conname AS object_name
FROM resolved_relations
JOIN pg_constraint AS constraints_catalog
  ON constraints_catalog.conrelid = resolved_relations.relation_oid
`, tables).Scan(&constraintRows).Error; err != nil {
		if isSchemaCheckTimeout(ctx, err) {
			return schemaError("schema_check_timeout")
		}
		return schemaError("schema_check_unavailable")
	}

	var indexRows []schemaObjectMetadataRow
	if err := db.WithContext(ctx).Raw(resolvedTables+`
SELECT resolved_relations.table_name,
       resolved_relations.table_schema,
       indexes.indexname AS object_name
FROM resolved_relations
JOIN pg_indexes AS indexes
  ON indexes.schemaname = resolved_relations.table_schema
 AND indexes.tablename = resolved_relations.table_name
`, tables).Scan(&indexRows).Error; err != nil {
		if isSchemaCheckTimeout(ctx, err) {
			return schemaError("schema_check_timeout")
		}
		return schemaError("schema_check_unavailable")
	}

	return validateSchemaObjects(postSchemaObjectCanaries, constraintRows, indexRows)
}

func validateSchemaObjects(canaries []schemaObjectCanary, constraintRows, indexRows []schemaObjectMetadataRow) error {
	constraints := make(map[string]map[string]struct{}, len(constraintRows))
	for _, row := range constraintRows {
		if constraints[row.TableName] == nil {
			constraints[row.TableName] = make(map[string]struct{})
		}
		constraints[row.TableName][row.ObjectName] = struct{}{}
	}
	indexes := make(map[string]map[string]struct{}, len(indexRows))
	for _, row := range indexRows {
		if indexes[row.TableName] == nil {
			indexes[row.TableName] = make(map[string]struct{})
		}
		indexes[row.TableName][row.ObjectName] = struct{}{}
	}
	for _, canary := range canaries {
		for _, constraint := range canary.Constraints {
			if _, exists := constraints[canary.Table][constraint]; !exists {
				return schemaError("schema_constraint_missing")
			}
		}
		for _, index := range canary.Indexes {
			if _, exists := indexes[canary.Table][index]; !exists {
				return schemaError("schema_index_missing")
			}
		}
	}
	return nil
}

func validateLegacySchemaAbsence(ctx context.Context, db *gorm.DB) error {
	if err := ctx.Err(); err != nil {
		return schemaError("schema_check_timeout")
	}

	var tableRows []schemaMetadataRow
	if err := db.WithContext(ctx).Raw(`
SELECT class.relname AS table_name,
       namespace.nspname AS table_schema,
       class.relkind::text AS relation_kind
FROM pg_namespace AS namespace
JOIN pg_class AS class
  ON class.relnamespace = namespace.oid
WHERE namespace.nspname = ANY(current_schemas(false))
  AND class.relname IN ?
`, legacyContentTableNames).Scan(&tableRows).Error; err != nil {
		if isSchemaCheckTimeout(ctx, err) {
			return schemaError("schema_check_timeout")
		}
		return schemaError("schema_check_unavailable")
	}

	var columnRows []schemaMetadataRow
	legacyColumnPredicates := make([]string, 0, len(legacyRuntimeColumns))
	legacyColumnArgs := make([]interface{}, 0, len(legacyRuntimeColumns)*2)
	for _, legacyColumn := range legacyRuntimeColumns {
		legacyColumnPredicates = append(legacyColumnPredicates, "(class.relname = ? AND attribute.attname = ?)")
		legacyColumnArgs = append(legacyColumnArgs, legacyColumn.Table, legacyColumn.Column)
	}
	legacyColumnQuery := `
SELECT class.relname AS table_name,
       namespace.nspname AS table_schema,
       class.relkind::text AS relation_kind,
       attribute.attname AS column_name
FROM pg_namespace AS namespace
JOIN pg_class AS class
  ON class.relnamespace = namespace.oid
JOIN pg_attribute AS attribute
  ON attribute.attrelid = class.oid
 AND attribute.attnum > 0
 AND NOT attribute.attisdropped
WHERE namespace.nspname = ANY(current_schemas(false))
  AND (` + strings.Join(legacyColumnPredicates, " OR ") + `)
`
	if err := db.WithContext(ctx).Raw(legacyColumnQuery, legacyColumnArgs...).Scan(&columnRows).Error; err != nil {
		if isSchemaCheckTimeout(ctx, err) {
			return schemaError("schema_check_timeout")
		}
		return schemaError("schema_check_unavailable")
	}

	return validateLegacySchemaMetadata(tableRows, columnRows)
}

func validateLegacySchemaMetadata(tableRows, columnRows []schemaMetadataRow) error {
	if len(tableRows) > 0 || len(columnRows) > 0 {
		return schemaError("schema_legacy_content_present")
	}
	return nil
}

type resolvedSchemaMetadata struct {
	tableSchema  string
	relationKind string
	columns      map[string]struct{}
}

func validateResolvedSchema(canaries []schemaCanary, rows []schemaMetadataRow) error {
	resolved := make(map[string]resolvedSchemaMetadata, len(rows))
	for _, row := range rows {
		current, exists := resolved[row.TableName]
		if !exists {
			current = resolvedSchemaMetadata{
				tableSchema:  row.TableSchema,
				relationKind: row.RelationKind,
				columns:      make(map[string]struct{}),
			}
		} else if current.tableSchema != row.TableSchema || current.relationKind != row.RelationKind {
			// The query resolves one relation per table name. If a caller supplies
			// rows from another schema, never merge its columns into the selected
			// relation.
			continue
		}
		if row.ColumnName != "" {
			current.columns[row.ColumnName] = struct{}{}
		}
		resolved[row.TableName] = current
	}
	for _, canary := range canaries {
		metadata, exists := resolved[canary.Table]
		if !exists {
			return schemaError("schema_table_missing")
		}
		if metadata.relationKind != "r" && metadata.relationKind != "p" {
			return schemaError("schema_relation_invalid")
		}
		for _, column := range canary.Columns {
			if _, exists := metadata.columns[column]; !exists {
				return schemaError("schema_column_missing")
			}
		}
	}
	return nil
}

func updateRuntimeSchemaState(tx *gorm.DB, releaseRevision string) error {
	if tx == nil {
		return errors.New("database transaction is not initialized")
	}
	if err := validatePublishedSchemaVersions(); err != nil {
		return err
	}
	releaseRevision = strings.TrimSpace(releaseRevision)
	if releaseRevision == "" {
		releaseRevision = "unknown"
	}
	state := models.RuntimeSchemaState{
		ID:                 runtimeSchemaStateID,
		CurrentVersion:     PublishedSchemaCurrentVersion,
		CompatibilityFloor: PublishedSchemaCompatibilityFloor,
		AppliedAt:          time.Now().UTC(),
		ReleaseRevision:    releaseRevision,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"current_version", "compatibility_floor", "applied_at", "release_revision",
		}),
	}).Create(&state).Error
}

func runtimeReleaseRevision() string {
	for _, key := range []string{"RELEASE_REVISION", "GIT_COMMIT", "BUILD_REVISION"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "unknown"
}

func validateMigratedSchema(tx *gorm.DB) error {
	if err := validatePublishedSchemaVersions(); err != nil {
		return fmt.Errorf("validate published schema versions: %w", err)
	}
	options := SchemaValidationOptions{
		RequiredVersion:     RequiredSchemaVersion,
		IncludeWorkerTables: true,
	}
	if err := validateSchemaCanaries(context.Background(), tx, options); err != nil {
		return fmt.Errorf("post-migration schema validation: %w", err)
	}
	if err := updateRuntimeSchemaState(tx, runtimeReleaseRevision()); err != nil {
		return fmt.Errorf("publish runtime schema state: %w", err)
	}
	return nil
}

func validatePublishedSchemaVersions() error {
	if PublishedSchemaCurrentVersion < 1 || PublishedSchemaCompatibilityFloor < 1 ||
		PublishedSchemaCompatibilityFloor > PublishedSchemaCurrentVersion ||
		PublishedSchemaCompatibilityFloor > RequiredSchemaVersion ||
		RequiredSchemaVersion > PublishedSchemaCurrentVersion {
		return schemaError("schema_incompatible")
	}
	return nil
}

func isUndefinedTableError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

func isSchemaCheckTimeout(ctx context.Context, err error) bool {
	return (ctx != nil && ctx.Err() != nil) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
