package controllers

import (
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
)

const (
	defaultRecommendationLimit = 20
	maxRecommendationLimit     = 50
	recommendationCandidateCap = 200
	behaviorCountCap           = 5
)

var defaultRecommendationConfig = config.RecommendationConfig{
	BehaviorWeights: config.RecommendationBehaviorWeights{
		View: 1,
		Like: 4,
	},
	CategoryWeight:   3,
	TagWeight:        2,
	PopularityWeight: 0.5,
	FreshnessWeight:  1,
}

type articleBehaviorSignal struct {
	Behavior models.ArticleBehavior
	Article  models.Article
}

type userInterestProfile struct {
	Categories           map[string]float64
	Tags                 map[string]float64
	InteractedArticleIDs map[uint]struct{}
}

type recommendedArticleResponse struct {
	ID        uint      `json:"id"`
	Title     string    `json:"title"`
	Preview   string    `json:"preview"`
	Summary   string    `json:"summary"`
	Tags      []string  `json:"tags"`
	Category  string    `json:"category"`
	LikeCount int64     `json:"like_count"`
	CreatedAt time.Time `json:"created_at"`
	Score     float64   `json:"score"`
}

func normalizedRecommendationConfig() config.RecommendationConfig {
	cfg := defaultRecommendationConfig
	if config.AppConfig == nil {
		return cfg
	}

	configured := config.AppConfig.Recommendation
	if configured.BehaviorWeights.View > 0 {
		cfg.BehaviorWeights.View = configured.BehaviorWeights.View
	}
	if configured.BehaviorWeights.Like > 0 {
		cfg.BehaviorWeights.Like = configured.BehaviorWeights.Like
	}
	if configured.CategoryWeight > 0 {
		cfg.CategoryWeight = configured.CategoryWeight
	}
	if configured.TagWeight > 0 {
		cfg.TagWeight = configured.TagWeight
	}
	if configured.PopularityWeight > 0 {
		cfg.PopularityWeight = configured.PopularityWeight
	}
	if configured.FreshnessWeight > 0 {
		cfg.FreshnessWeight = configured.FreshnessWeight
	}
	return cfg
}

func recommendationActionWeight(cfg config.RecommendationConfig, action string) (float64, bool) {
	switch action {
	case ArticleBehaviorActionView:
		return cfg.BehaviorWeights.View, true
	case ArticleBehaviorActionLike:
		return cfg.BehaviorWeights.Like, true
	default:
		return 0, false
	}
}

var loadRecommendationBehaviorSignals = func(userID uint) ([]articleBehaviorSignal, error) {
	if userID == 0 {
		return nil, nil
	}
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}

	var behaviors []models.ArticleBehavior
	if err := global.Db.
		Where("user_id = ? AND active = ? AND action IN ?", userID, true, []string{ArticleBehaviorActionView, ArticleBehaviorActionLike}).
		Find(&behaviors).Error; err != nil {
		return nil, err
	}
	if len(behaviors) == 0 {
		return nil, nil
	}

	articleIDs := make([]uint, 0, len(behaviors))
	seenIDs := make(map[uint]struct{}, len(behaviors))
	for _, behavior := range behaviors {
		if behavior.ArticleID == 0 {
			continue
		}
		if _, exists := seenIDs[behavior.ArticleID]; exists {
			continue
		}
		seenIDs[behavior.ArticleID] = struct{}{}
		articleIDs = append(articleIDs, behavior.ArticleID) //得到去重之后的文章id
	}
	if len(articleIDs) == 0 {
		return nil, nil
	}

	var articles []models.Article
	if err := global.Db.
		Select("id,tags,category").
		Where("id IN ?", articleIDs).
		Find(&articles).Error; err != nil {
		return nil, err
	} //查询文章类型

	articleByID := make(map[uint]models.Article, len(articles))
	for _, article := range articles {
		articleByID[article.ID] = article
	} //根据行为里面包含的文章ID可以快速查找到文章的详情

	signals := make([]articleBehaviorSignal, 0, len(behaviors))
	for _, behavior := range behaviors {
		article, exists := articleByID[behavior.ArticleID]
		if !exists {
			continue
		}
		signals = append(signals, articleBehaviorSignal{
			Behavior: behavior,
			Article:  article,
		})
	}
	return signals, nil
} //包括用户行为和文章类型，和文章的id

var loadRecommendationCandidates = func(excludedArticleIDs map[uint]struct{}, now time.Time) ([]models.Article, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}

	query := global.Db.
		Select("id,title,preview,summary,tags,category,like_count,created_at,updated_at,deleted_at").
		Where("status = ?", consts.ArticleStatusCompleted).
		Where("expired_at > ? OR expired_at IS NULL", now).
		Order("created_at desc").
		Limit(recommendationCandidateCap) //先构建好查询语句模板

	excludedIDs := articleIDList(excludedArticleIDs)
	if len(excludedIDs) > 0 {
		query = query.Where("id NOT IN ?", excludedIDs)
	} //排除已经看过的文章

	var articles []models.Article
	return articles, query.Find(&articles).Error
}

func GetArticleRecommendations(ctx *gin.Context) {
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	limit := parseRecommendationLimit(ctx.Query("limit"))
	signals, err := loadRecommendationBehaviorSignals(userID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	profile := buildUserInterestProfile(signals)
	now := time.Now()
	candidates, err := loadRecommendationCandidates(profile.InteractedArticleIDs, now)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, recommendArticles(profile, candidates, now, limit))
}

func parseRecommendationLimit(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return defaultRecommendationLimit
	}

	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return defaultRecommendationLimit
	}
	if limit > maxRecommendationLimit {
		return maxRecommendationLimit
	}
	return limit
}

func buildUserInterestProfile(signals []articleBehaviorSignal) userInterestProfile {
	profile := userInterestProfile{
		Categories:           map[string]float64{},
		Tags:                 map[string]float64{},
		InteractedArticleIDs: map[uint]struct{}{},
	}
	recommendationCfg := normalizedRecommendationConfig()

	for _, signal := range signals {
		if !signal.Behavior.Active {
			continue
		}
		if signal.Behavior.ArticleID != 0 {
			profile.InteractedArticleIDs[signal.Behavior.ArticleID] = struct{}{}
		} //K是对应文章id然后V设为空，好让后面查询的时候不会查到这些文章

		actionWeight, ok := recommendationActionWeight(recommendationCfg, signal.Behavior.Action)
		if !ok {
			continue
		}
		weightedCount := signal.Behavior.Count
		if weightedCount <= 0 {
			weightedCount = 1
		}
		if weightedCount > behaviorCountCap {
			weightedCount = behaviorCountCap
		}
		weight := actionWeight * float64(weightedCount)
		//算分
		//分别给文章类型和标签加权
		if category := normalizeRecommendationLabel(signal.Article.Category); category != "" {
			profile.Categories[category] += weight
		}
		for _, tag := range signal.Article.Tags {
			if normalizedTag := normalizeRecommendationLabel(tag); normalizedTag != "" {
				profile.Tags[normalizedTag] += weight
			}
		}
	}

	return profile
}

func recommendArticles(profile userInterestProfile, candidates []models.Article, now time.Time, limit int) []recommendedArticleResponse {
	if limit <= 0 {
		limit = defaultRecommendationLimit
	}
	if limit > maxRecommendationLimit {
		limit = maxRecommendationLimit
	}

	recommendations := make([]recommendedArticleResponse, 0, len(candidates))
	for _, article := range candidates {
		if _, interacted := profile.InteractedArticleIDs[article.ID]; interacted {
			continue
		}

		recommendations = append(recommendations, recommendedArticleResponse{
			ID:        article.ID,
			Title:     article.Title,
			Preview:   article.Preview,
			Summary:   article.Summary,
			Tags:      article.Tags,
			Category:  article.Category,
			LikeCount: article.LikeCount,
			CreatedAt: article.CreatedAt,
			Score:     scoreArticle(profile, article, now),
		})
	}

	sort.SliceStable(recommendations, func(i, j int) bool {
		left := recommendations[i]
		right := recommendations[j]
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		return left.ID > right.ID
	})

	if len(recommendations) > limit {
		return recommendations[:limit]
	}
	return recommendations
}

func scoreArticle(profile userInterestProfile, article models.Article, now time.Time) float64 {
	categoryMatch := profile.Categories[normalizeRecommendationLabel(article.Category)]
	tagMatch := 0.0
	for _, tag := range article.Tags {
		tagMatch += profile.Tags[normalizeRecommendationLabel(tag)]
	}
	recommendationCfg := normalizedRecommendationConfig()

	return categoryMatch*recommendationCfg.CategoryWeight +
		tagMatch*recommendationCfg.TagWeight +
		math.Log(float64(article.LikeCount)+1)*recommendationCfg.PopularityWeight +
		freshnessScore(article.CreatedAt, now)*recommendationCfg.FreshnessWeight
} //根据类型标签热度外加时间权重给出分数

func freshnessScore(createdAt time.Time, now time.Time) float64 {
	age := now.Sub(createdAt)
	switch {
	case age <= 7*24*time.Hour:
		return 1
	case age <= 30*24*time.Hour:
		return 0.5
	default:
		return 0
	}
} //根据时间算分

func normalizeRecommendationLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(label))
}

func articleIDList(set map[uint]struct{}) []uint {
	if len(set) == 0 {
		return nil
	}

	ids := make([]uint, 0, len(set))
	for id := range set {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})
	return ids
} //把文章id转化成数组
