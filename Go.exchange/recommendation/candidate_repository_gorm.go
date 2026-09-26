package recommendation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const recommendationPublicPostColumns = "posts.id,posts.created_at,posts.updated_at,posts.author_id,posts.content,posts.language,posts.reply_to_post_id,posts.quote_post_id,posts.conversation_id,posts.visibility,posts.like_count,posts.reply_count,posts.view_count,posts.like_sync_version,posts.deleted_at"

type GormCandidateRepository struct {
	db *gorm.DB
}

func NewGormCandidateRepository(db *gorm.DB) (*GormCandidateRepository, error) {
	if db == nil {
		return nil, errors.New("recommendation candidate repository database is nil")
	}
	return &GormCandidateRepository{db: db}, nil
}

func recommendationContextDB(ctx context.Context, db *gorm.DB) (*gorm.DB, error) {
	if ctx == nil {
		return nil, errors.New("recommendation repository context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, errors.New("recommendation repository database is nil")
	}
	return db.WithContext(ctx), nil
}

func recommendationPostIDList(set map[uint]struct{}) []uint {
	ids := make([]uint, 0, len(set))
	for id := range set {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func recommendationPublicPostScope(query *gorm.DB) *gorm.DB {
	return query.Where(`
posts.deleted_at IS NULL
AND posts.visibility = 'public'
AND EXISTS (
    SELECT 1 FROM users AS post_author
    WHERE post_author.id = posts.author_id
      AND post_author.deleted_at IS NULL
)
`)
}

func (r *GormCandidateRepository) eligibilityQuery(db *gorm.DB, query *gorm.DB, input CandidateQuery) *gorm.DB {
	negative := db.Table("post_behaviors AS ni").
		Select("1").
		Where("ni.user_id = ? AND ni.post_id = posts.id AND ni.action = ? AND ni.active = TRUE",
			input.UserID, eventing.RecommendationBehaviorActionNotInterested)
	laterLike := db.Table("post_reaction AS ar").
		Select("1").
		Where("ar.user_id = ? AND ar.post_id = ni.post_id AND ar.liked = TRUE AND ar.state_changed_at > ni.last_seen_at", input.UserID)
	laterReply := db.Table("post_behaviors AS rb").
		Select("1").
		Where("rb.user_id = ? AND rb.post_id = ni.post_id AND rb.action = ? AND rb.active = TRUE AND rb.last_seen_at > ni.last_seen_at",
			input.UserID, "reply")
	negative = negative.Where("NOT EXISTS (?)", laterLike).Where("NOT EXISTS (?)", laterReply)
	query = recommendationPublicPostScope(query).
		Where("posts.reply_to_post_id IS NULL").
		Where("posts.author_id <> ?", input.UserID).
		Where("NOT EXISTS (?)", negative)
	if input.MaterializedInteractionsReady {
		interacted := db.Table("user_post_reco_states AS rs").
			Select("1").
			Where("rs.user_id = ? AND rs.post_id = posts.id AND rs.interacted = TRUE", input.UserID)
		query = query.Where("NOT EXISTS (?)", interacted)
	} else if ids := recommendationPostIDList(input.InteractedPostIDs); len(ids) > 0 {
		query = query.Where("posts.id NOT IN ?", ids)
	}

	if input.SoftOnly {
		softIDs := make([]uint, 0, len(input.Served))
		for id, item := range input.Served {
			if id != 0 && item.Soft && !item.Hard {
				softIDs = append(softIDs, id)
			}
		}
		if len(softIDs) == 0 {
			return query.Where("1 = 0")
		}
		query = query.Where("posts.id IN ?", softIDs)
	} else if len(input.Served) > 0 {
		query = query.Where("posts.id NOT IN ?", recommendationPostIDList(servedPostIDs(input.Served)))
	}
	return query
}

func servedPostIDs(history map[uint]ServedItem) map[uint]struct{} {
	ids := make(map[uint]struct{}, len(history))
	for id := range history {
		ids[id] = struct{}{}
	}
	return ids
}

func (r *GormCandidateRepository) LoadSemanticCandidates(ctx context.Context, input CandidateQuery) ([]Candidate, error) {
	if len(input.PositiveVector) == 0 || input.Limit <= 0 {
		return nil, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("load semantic candidates: %w", err)
	}
	recentLimit, evergreenLimit := SemanticRecallQuota(input.Limit, input.SemanticRecentRatio)
	recent, err := r.loadSemanticPool(db, input, input.SemanticRecentCutoff, ">=", recentLimit, nil)
	if err != nil {
		return nil, fmt.Errorf("load recent semantic candidates: %w", err)
	}
	selected := make(map[uint]struct{}, len(recent)+evergreenLimit)
	for _, candidate := range recent {
		selected[candidate.PostID] = struct{}{}
	}
	evergreen, err := r.loadSemanticPool(db, input, input.SemanticRecentCutoff, "<", evergreenLimit, selected)
	if err != nil {
		return nil, fmt.Errorf("load evergreen semantic candidates: %w", err)
	}
	result := make([]Candidate, 0, input.Limit)
	result = append(result, recent...)
	for _, candidate := range evergreen {
		selected[candidate.PostID] = struct{}{}
	}
	result = append(result, evergreen...)
	remaining := input.Limit - len(result)
	if remaining > 0 {
		backfill, err := r.loadSemanticPool(db, input, time.Time{}, "", remaining, selected)
		if err != nil {
			return nil, fmt.Errorf("backfill semantic candidates: %w", err)
		}
		result = append(result, backfill...)
	}
	return result, nil
}

type semanticCandidateRow struct {
	PostID                     uint
	PositiveSemanticSimilarity float64
}

func (r *GormCandidateRepository) loadSemanticPool(db *gorm.DB, input CandidateQuery, cutoff time.Time, comparison string, limit int, excluded map[uint]struct{}) ([]Candidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	queryVector := pgvector.NewVector(input.PositiveVector)
	query := r.eligibilityQuery(db, db.Table("post_embeddings AS ae").
		Select("ae.post_id, 1 - (ae.embedding <=> ?) AS positive_semantic_similarity", queryVector).
		Joins("JOIN posts ON posts.id = ae.post_id").
		Where("ae.version = ? AND ae.dimensions = ?", input.ServingVersion, len(input.PositiveVector)), input)
	if !cutoff.IsZero() && comparison != "" {
		query = query.Where("posts.created_at "+comparison+" ?", cutoff)
	}
	if ids := recommendationPostIDList(excluded); len(ids) > 0 {
		query = query.Where("posts.id NOT IN ?", ids)
	}
	var rows []semanticCandidateRow
	if err := query.Clauses(clause.OrderBy{Expression: clause.Expr{
		SQL: "ae.embedding <=> ? ASC, posts.id DESC", Vars: []interface{}{queryVector},
	}}).Limit(limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, Candidate{PostID: row.PostID, PositiveSemanticSimilarity: ClampSemanticSimilarity(row.PositiveSemanticSimilarity), FromSemantic: true})
	}
	return result, nil
}

func (r *GormCandidateRepository) LoadFollowingCandidates(ctx context.Context, input CandidateQuery) ([]Candidate, error) {
	if input.Limit <= 0 {
		return nil, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("load following candidates: %w", err)
	}
	query := r.eligibilityQuery(db, db.Table("posts").Select("posts.id").
		Joins("JOIN user_follows AS uf ON uf.following_id = posts.author_id AND uf.follower_id = ?", input.UserID), input)
	return queryCandidateIDs(query.Order("posts.created_at DESC, posts.id DESC"), input.Limit, true, false)
}

func (r *GormCandidateRepository) LoadRecentCandidates(ctx context.Context, input CandidateQuery) ([]Candidate, error) {
	if input.Limit <= 0 {
		return nil, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("load recent candidates: %w", err)
	}
	query := r.eligibilityQuery(db, db.Table("posts").Select("posts.id"), input)
	return queryCandidateIDs(query.Order("posts.created_at DESC, posts.id DESC"), input.Limit, false, true)
}

func (r *GormCandidateRepository) LoadTrendingCandidates(ctx context.Context, input CandidateQuery) ([]Candidate, error) {
	if input.Limit <= 0 {
		return nil, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("load trending candidates: %w", err)
	}
	query := r.eligibilityQuery(db, db.Table("posts").Select("posts.id"), input).
		Where("posts.created_at >= ?", input.TrendingCutoff).
		Where("posts.like_count > 0 OR posts.reply_count > 0")
	order := trendingOrder(input.Now, input.TrendingReplyFactor, input.TrendingHalfLifeHours)
	return queryCandidateIDs(query.Order(order), input.Limit, false, false)
}

func (r *GormCandidateRepository) LoadPublicRecentCandidates(ctx context.Context, input PublicCandidateQuery) ([]Candidate, error) {
	if input.Limit <= 0 {
		return nil, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("load public recent candidates: %w", err)
	}
	query := publicCandidateQuery(db, input)
	return queryCandidateIDs(query.Order("posts.created_at DESC, posts.id DESC"), input.Limit, false, true)
}

func (r *GormCandidateRepository) LoadPublicTrendingCandidates(ctx context.Context, input PublicCandidateQuery) ([]Candidate, error) {
	if input.Limit <= 0 {
		return nil, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("load public trending candidates: %w", err)
	}
	query := publicCandidateQuery(db, input).
		Where("posts.created_at >= ?", input.TrendingCutoff).
		Where("posts.like_count > 0 OR posts.reply_count > 0")
	return queryCandidateIDs(query.Order(trendingOrder(input.Now, input.TrendingReplyFactor, input.TrendingHalfLifeHours)), input.Limit, false, false)
}

func publicCandidateQuery(db *gorm.DB, input PublicCandidateQuery) *gorm.DB {
	query := recommendationPublicPostScope(db.Table("posts").Select("posts.id")).Where("posts.reply_to_post_id IS NULL")
	if ids := recommendationPostIDList(input.ExcludedPostIDs); len(ids) > 0 {
		query = query.Where("posts.id NOT IN ?", ids)
	}
	return query
}

func trendingOrder(now time.Time, replyFactor, halfLifeHours float64) clause.Expr {
	return clause.Expr{SQL: `
(
    LN(1 + GREATEST(posts.like_count, 0))
    + ? * LN(1 + GREATEST(posts.reply_count, 0))
)
*
EXP(
    -LN(2)
    * GREATEST(EXTRACT(EPOCH FROM (? - posts.created_at)) / 3600.0, 0)
    / ?
)
DESC,
posts.created_at DESC,
posts.id DESC`, Vars: []interface{}{replyFactor, now.UTC(), halfLifeHours}}
}

func queryCandidateIDs(query *gorm.DB, limit int, following, recent bool) ([]Candidate, error) {
	var ids []uint
	if err := query.Limit(limit).Pluck("posts.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]Candidate, 0, len(ids))
	for _, id := range ids {
		candidate := Candidate{PostID: id, FromFollowing: following, FromRecent: recent, FromTrending: !following && !recent}
		result = append(result, candidate)
	}
	return result, nil
}

func (r *GormCandidateRepository) HydrateCandidates(ctx context.Context, servingVersion string, candidates []Candidate, now time.Time) ([]RankedCandidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("hydrate recommendation candidates: %w", err)
	}
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.PostID != 0 {
			ids = append(ids, candidate.PostID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var posts []models.Post
	query := recommendationPublicPostScope(db.Model(&models.Post{}).Select(recommendationPublicPostColumns).Where("posts.id IN ?", ids))
	if err := query.Preload("Author", func(tx *gorm.DB) *gorm.DB { return tx.Select("id, username, display_name, avatar_url") }).Find(&posts).Error; err != nil {
		return nil, fmt.Errorf("load candidate posts: %w", err)
	}
	byID := make(map[uint]models.Post, len(posts))
	validIDs := make([]uint, 0, len(posts))
	for _, post := range posts {
		if post.AuthorID == 0 || post.Author.ID == 0 || post.Author.ID != post.AuthorID {
			continue
		}
		byID[post.ID] = post
		validIDs = append(validIDs, post.ID)
	}
	if len(validIDs) == 0 {
		return nil, nil
	}
	embeddings, err := r.LoadPostEmbeddings(ctx, validIDs, servingVersion)
	if err != nil {
		return nil, err
	}
	result := make([]RankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		post, ok := byID[candidate.PostID]
		if !ok {
			continue
		}
		embedding := embeddings[candidate.PostID]
		if len(embedding) == 0 {
			embedding = nil
		}
		result = append(result, RankedCandidate{Candidate: candidate, Post: post, Embedding: embedding})
	}
	return result, nil
}

func (r *GormCandidateRepository) LoadPostEmbeddings(ctx context.Context, postIDs []uint, version string) (map[uint][]float32, error) {
	result := make(map[uint][]float32)
	if len(postIDs) == 0 {
		return result, nil
	}
	db, err := recommendationContextDB(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("load post embeddings: %w", err)
	}
	var rows []models.PostEmbedding
	if err := db.Select("post_id, embedding").Where("post_id IN ? AND version = ?", postIDs, version).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load post embeddings: %w", err)
	}
	for _, row := range rows {
		result[row.PostID] = append([]float32(nil), row.Embedding.Slice()...)
	}
	return result, nil
}

func (r *GormCandidateRepository) String() string {
	return fmt.Sprintf("%T", r)
}

var _ CandidateRepository = (*GormCandidateRepository)(nil)
