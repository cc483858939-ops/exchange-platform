package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	postSearchDefaultLimit     = 20
	postSearchMaxLimit         = 50
	postSearchMaxSafeID        = uint64(1<<53 - 1)
	postSearchContentPredicate = `posts.content ILIKE ? ESCAPE '\'`
)

type postSearchCriteria struct {
	Query    string
	AuthorID *uint
	From     *time.Time
	To       *time.Time
	Sort     string
	Limit    int
}

type postSearchCandidate struct {
	ID        uint
	CreatedAt time.Time
}

func SearchPosts(ctx *gin.Context) {
	criteria, err := parsePostSearchCriteria(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	criteriaHash, err := postSearchCriteriaHash(cursorCriteriaForPostSearch(criteria))
	if err != nil {
		writePostTimelineStoreError(ctx, err)
		return
	}

	var cursor *postSearchCursorV1
	if raw, present := ctx.GetQuery("cursor"); present {
		parsed, err := decodePostSearchCursor(raw)
		if err != nil || parsed.CriteriaHash != criteriaHash {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid cursor"})
			return
		}
		cursor = &parsed
	}

	anchorAt := time.Now().UTC()
	if cursor != nil {
		anchorAt = cursor.AnchorAt.UTC()
		if cursor.CreatedAt.After(anchorAt) {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid cursor"})
			return
		}
	}
	if global.APIDb == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "database is not initialized"})
		return
	}

	searchCtx, cancel := context.WithTimeout(ctx.Request.Context(), config.PostSearchTimeout())
	defer cancel()
	page, err := loadPostSearchPage(searchCtx, global.APIDb, criteria, criteriaHash, anchorAt, cursor)
	if err != nil {
		writePostTimelineStoreError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, page)
}

func parsePostSearchCriteria(ctx *gin.Context) (postSearchCriteria, error) {
	query := strings.TrimSpace(ctx.Query("q"))
	runeCount := utf8.RuneCountInString(query)
	if runeCount < 2 || runeCount > 200 {
		return postSearchCriteria{}, errors.New("q must contain between 2 and 200 characters")
	}

	criteria := postSearchCriteria{Query: query, Sort: "latest", Limit: postSearchDefaultLimit}
	if sort := ctx.Query("sort"); sort != "" {
		if sort != "latest" {
			return postSearchCriteria{}, errors.New("sort must be latest")
		}
		criteria.Sort = sort
	}
	if raw := ctx.Query("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > postSearchMaxLimit {
			return postSearchCriteria{}, errors.New("limit must be between 1 and 50")
		}
		criteria.Limit = limit
	}
	if raw := ctx.Query("author_id"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 || id > postSearchMaxSafeID || uint64(uint(id)) != id {
			return postSearchCriteria{}, errors.New("author_id must be a positive safe integer")
		}
		authorID := uint(id)
		criteria.AuthorID = &authorID
	}
	if raw := ctx.Query("from"); raw != "" {
		from, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return postSearchCriteria{}, errors.New("from must be an RFC3339 timestamp")
		}
		from = from.UTC()
		criteria.From = &from
	}
	if raw := ctx.Query("to"); raw != "" {
		to, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return postSearchCriteria{}, errors.New("to must be an RFC3339 timestamp")
		}
		to = to.UTC()
		criteria.To = &to
	}
	if criteria.From != nil && criteria.To != nil && !criteria.From.Before(*criteria.To) {
		return postSearchCriteria{}, errors.New("from must be before to")
	}
	return criteria, nil
}

func cursorCriteriaForPostSearch(criteria postSearchCriteria) postSearchCursorCriteria {
	var from, to *string
	if criteria.From != nil {
		value := criteria.From.UTC().Format(time.RFC3339Nano)
		from = &value
	}
	if criteria.To != nil {
		value := criteria.To.UTC().Format(time.RFC3339Nano)
		to = &value
	}
	return postSearchCursorCriteria{
		Query:    criteria.Query,
		AuthorID: criteria.AuthorID,
		From:     from,
		To:       to,
		Sort:     criteria.Sort,
	}
}

func loadPostSearchPage(
	ctx context.Context,
	db *gorm.DB,
	criteria postSearchCriteria,
	criteriaHash string,
	anchorAt time.Time,
	cursor *postSearchCursorV1,
) (postPageResponse, error) {
	if ctx == nil || db == nil {
		return postPageResponse{}, errors.New("post search database is not initialized")
	}
	query := db.WithContext(ctx).
		Model(&models.Post{}).
		Select("posts.id, posts.created_at").
		Where(publicPostEligibilitySQL("posts")).
		Where(postSearchContentPredicate, postSearchLikePattern(criteria.Query)).
		Where("posts.created_at <= ?", anchorAt.UTC())
	if criteria.AuthorID != nil {
		query = query.Where("posts.author_id = ?", *criteria.AuthorID)
	}
	if criteria.From != nil {
		query = query.Where("posts.created_at >= ?", criteria.From.UTC())
	}
	if criteria.To != nil {
		query = query.Where("posts.created_at < ?", criteria.To.UTC())
	}
	if cursor != nil {
		query = query.Where(
			"(posts.created_at < ?) OR (posts.created_at = ? AND posts.id < ?)",
			cursor.CreatedAt.UTC(),
			cursor.CreatedAt.UTC(),
			cursor.PostID,
		)
	}
	candidates := make([]postSearchCandidate, 0, criteria.Limit+1)
	if err := query.Order("posts.created_at DESC, posts.id DESC").Limit(criteria.Limit + 1).Find(&candidates).Error; err != nil {
		return postPageResponse{}, err
	}
	hasMore := len(candidates) > criteria.Limit
	if hasMore {
		candidates = candidates[:criteria.Limit]
	}
	items := make([]postResponse, 0, len(candidates))
	if len(candidates) > 0 {
		ids := make([]uint, 0, len(candidates))
		for _, candidate := range candidates {
			ids = append(ids, candidate.ID)
		}
		posts := make([]models.Post, 0, len(ids))
		postQuery := preloadPostAuthor(db.WithContext(ctx).Model(&models.Post{})).
			Select(publicPostSelectColumns).
			Where("posts.id IN ?", ids)
		if err := publicPostScope(postQuery, time.Now().UTC()).Find(&posts).Error; err != nil {
			return postPageResponse{}, err
		}
		responses, err := newPostResponses(posts)
		if err != nil {
			return postPageResponse{}, err
		}
		responseByPostID := make(map[uint]postResponse, len(responses))
		for _, response := range responses {
			responseByPostID[response.ID] = response
		}
		items = make([]postResponse, 0, len(responses))
		for _, candidate := range candidates {
			if response, ok := responseByPostID[candidate.ID]; ok {
				items = append(items, response)
			}
		}
		if err := hydratePostResponsesMediaFromDB(db.WithContext(ctx), items); err != nil {
			return postPageResponse{}, err
		}
		if err := hydratePostResponseRepostCountsFromDB(db.WithContext(ctx), items); err != nil {
			return postPageResponse{}, err
		}
		if err := hydratePostResponsesReferencesFromDB(db.WithContext(ctx), items, time.Now().UTC()); err != nil {
			return postPageResponse{}, err
		}
	}

	var nextCursor *string
	if hasMore {
		last := candidates[len(candidates)-1]
		encoded, err := encodePostSearchCursor(postSearchCursorV1{
			Version: 1, CriteriaHash: criteriaHash, AnchorAt: anchorAt.UTC(),
			CreatedAt: last.CreatedAt.UTC(), PostID: last.ID,
		})
		if err != nil {
			return postPageResponse{}, fmt.Errorf("encode post search cursor: %w", err)
		}
		nextCursor = &encoded
	}
	return postPageResponse{Items: items, NextCursor: nextCursor}, nil
}

func postSearchLikePattern(query string) string {
	query = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	return "%" + query + "%"
}
