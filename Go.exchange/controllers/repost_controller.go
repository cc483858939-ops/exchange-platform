package controllers

import (
	"errors"
	"net/http"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errArticleRepostNotFound = errors.New("article not found")

type articleRepostStateResult struct {
	Reposts  int64
	Reposted bool
}

type articleRepostMutationResult = articleRepostStateResult

type articleRepostStatesRequest struct {
	ArticleIDs []uint `json:"article_ids"`
}

type articleRepostStateItem struct {
	ArticleID uint  `json:"article_id"`
	Reposts   int64 `json:"reposts"`
	Reposted  bool  `json:"reposted"`
}

type articleRepostStatesResponse struct {
	Items                 []articleRepostStateItem `json:"items"`
	UnavailableArticleIDs []uint                   `json:"unavailable_article_ids"`
}

type articleRepostStatesLoadResult struct {
	States      map[uint]articleRepostStateResult
	Unavailable []uint
}

type articleRepostCountRow struct {
	ArticleID uint  `gorm:"column:article_id"`
	Reposts   int64 `gorm:"column:reposts"`
}

const maxArticleRepostStateIDs = 100

var loadArticleRepostState = loadArticleRepostStateFromDB
var mutateArticleRepost = mutateArticleRepostFromDB
var loadArticleRepostStates = loadArticleRepostStatesFromDB

func GetArticleRepostState(ctx *gin.Context) {
	articleID, ok := articleIDFromContext(ctx)
	if !ok {
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	result, err := loadArticleRepostState(userID, articleID)
	if err != nil {
		writeArticleRepostError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, articleRepostStatePayload(result))
}

// GetArticleRepost is kept as a descriptive handler alias for route wiring and tests.
func GetArticleRepost(ctx *gin.Context) {
	GetArticleRepostState(ctx)
}

func RepostArticle(ctx *gin.Context) {
	mutateArticleRepostRequest(ctx, true)
}

func UndoRepostArticle(ctx *gin.Context) {
	mutateArticleRepostRequest(ctx, false)
}

func mutateArticleRepostRequest(ctx *gin.Context, reposted bool) {
	articleID, ok := articleIDFromContext(ctx)
	if !ok {
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	result, err := mutateArticleRepost(userID, articleID, reposted)
	if err != nil {
		writeArticleRepostError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, articleRepostStatePayload(result))
}

func GetArticleRepostStates(ctx *gin.Context) {
	var request articleRepostStatesRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid article_ids"})
		return
	}
	if len(request.ArticleIDs) == 0 || len(request.ArticleIDs) > maxArticleRepostStateIDs {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "article_ids must contain between 1 and 100 ids"})
		return
	}

	uniqueIDs := make([]uint, 0, len(request.ArticleIDs))
	seen := make(map[uint]struct{}, len(request.ArticleIDs))
	for _, articleID := range request.ArticleIDs {
		if articleID == 0 {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "article_ids must contain positive ids"})
			return
		}
		if _, exists := seen[articleID]; exists {
			continue
		}
		seen[articleID] = struct{}{}
		uniqueIDs = append(uniqueIDs, articleID)
	}

	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}
	result, err := loadArticleRepostStates(userID, uniqueIDs)
	if err != nil {
		writeArticleRepostError(ctx, err)
		return
	}

	response := articleRepostStatesResponse{
		Items:                 make([]articleRepostStateItem, 0, len(result.States)),
		UnavailableArticleIDs: make([]uint, 0, len(result.Unavailable)),
	}
	unavailable := make(map[uint]struct{}, len(result.Unavailable))
	for _, articleID := range result.Unavailable {
		unavailable[articleID] = struct{}{}
	}
	for _, articleID := range uniqueIDs {
		if state, available := result.States[articleID]; available {
			response.Items = append(response.Items, articleRepostStateItem{
				ArticleID: articleID,
				Reposts:   normalizeArticleRepostCount(state.Reposts),
				Reposted:  state.Reposted,
			})
			continue
		}
		if _, markedUnavailable := unavailable[articleID]; markedUnavailable {
			response.UnavailableArticleIDs = append(response.UnavailableArticleIDs, articleID)
			continue
		}
		response.UnavailableArticleIDs = append(response.UnavailableArticleIDs, articleID)
	}
	ctx.JSON(http.StatusOK, response)
}

func articleRepostStatePayload(result articleRepostStateResult) gin.H {
	return gin.H{
		"reposts":  normalizeArticleRepostCount(result.Reposts),
		"reposted": result.Reposted,
	}
}

func normalizeArticleRepostCount(count int64) int64 {
	if count < 0 {
		return 0
	}
	return count
}

func writeArticleRepostError(ctx *gin.Context, err error) {
	if errors.Is(err, errArticleRepostNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "article not found"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

func loadArticleRepostStateFromDB(userID, articleID uint) (articleRepostStateResult, error) {
	if global.Db == nil {
		return articleRepostStateResult{}, errors.New("database is not initialized")
	}
	return loadArticleRepostStateWithDB(global.Db, userID, articleID, time.Now().UTC())
}

func loadArticleRepostStateWithDB(db *gorm.DB, userID, articleID uint, now time.Time) (articleRepostStateResult, error) {
	if err := requirePublicArticle(db, articleID, now); err != nil {
		return articleRepostStateResult{}, err
	}

	var reposts int64
	if err := db.Model(&models.ArticleRepost{}).
		Where("article_id = ?", articleID).
		Count(&reposts).Error; err != nil {
		return articleRepostStateResult{}, err
	}

	var relation models.ArticleRepost
	err := db.Where("user_id = ? AND article_id = ?", userID, articleID).
		Select("id").First(&relation).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return articleRepostStateResult{}, err
	}
	return articleRepostStateResult{
		Reposts:  normalizeArticleRepostCount(reposts),
		Reposted: err == nil,
	}, nil
}

func mutateArticleRepostFromDB(userID, articleID uint, reposted bool) (articleRepostMutationResult, error) {
	if global.Db == nil {
		return articleRepostMutationResult{}, errors.New("database is not initialized")
	}

	var result articleRepostMutationResult
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		if err := requirePublicArticle(tx, articleID, time.Now().UTC()); err != nil {
			return err
		}

		if reposted {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}, {Name: "article_id"}},
				DoNothing: true,
			}).Create(&models.ArticleRepost{UserID: userID, ArticleID: articleID}).Error; err != nil {
				return err
			}
		} else if err := tx.Where("user_id = ? AND article_id = ?", userID, articleID).
			Delete(&models.ArticleRepost{}).Error; err != nil {
			return err
		}

		var err error
		result, err = loadArticleRepostStateWithDB(tx, userID, articleID, time.Now().UTC())
		return err
	})
	return result, err
}

func loadArticleRepostStatesFromDB(userID uint, articleIDs []uint) (articleRepostStatesLoadResult, error) {
	result := articleRepostStatesLoadResult{
		States:      make(map[uint]articleRepostStateResult, len(articleIDs)),
		Unavailable: make([]uint, 0),
	}
	if global.Db == nil {
		return result, errors.New("database is not initialized")
	}
	if len(articleIDs) == 0 {
		return result, nil
	}

	now := time.Now().UTC()
	var availableIDs []uint
	if err := publicArticleScope(global.Db.Model(&models.Article{}), now).
		Where("articles.id IN ?", articleIDs).
		Pluck("articles.id", &availableIDs).Error; err != nil {
		return articleRepostStatesLoadResult{}, err
	}
	available := make(map[uint]struct{}, len(availableIDs))
	for _, articleID := range availableIDs {
		available[articleID] = struct{}{}
		result.States[articleID] = articleRepostStateResult{}
	}

	if len(availableIDs) > 0 {
		var counts []articleRepostCountRow
		if err := global.Db.Model(&models.ArticleRepost{}).
			Select("article_id, COUNT(*) AS reposts").
			Where("article_id IN ?", availableIDs).
			Group("article_id").
			Scan(&counts).Error; err != nil {
			return articleRepostStatesLoadResult{}, err
		}
		for _, count := range counts {
			state := result.States[count.ArticleID]
			state.Reposts = normalizeArticleRepostCount(count.Reposts)
			result.States[count.ArticleID] = state
		}

		var repostedIDs []uint
		if err := global.Db.Model(&models.ArticleRepost{}).
			Where("user_id = ? AND article_id IN ?", userID, availableIDs).
			Pluck("article_id", &repostedIDs).Error; err != nil {
			return articleRepostStatesLoadResult{}, err
		}
		for _, articleID := range repostedIDs {
			state := result.States[articleID]
			state.Reposted = true
			result.States[articleID] = state
		}
	}

	for _, articleID := range articleIDs {
		if _, ok := available[articleID]; !ok {
			result.Unavailable = append(result.Unavailable, articleID)
		}
	}
	return result, nil
}

func requirePublicArticle(db *gorm.DB, articleID uint, now time.Time) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	if articleID == 0 {
		return errArticleRepostNotFound
	}

	var id uint
	err := publicArticleScope(db.Model(&models.Article{}), now).
		Where("articles.id = ?", articleID).
		Select("articles.id").
		Take(&id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errArticleRepostNotFound
	}
	return err
}
