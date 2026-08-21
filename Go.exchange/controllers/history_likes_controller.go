package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type likedHistoryMembershipRow struct {
	ArticleID      uint      `gorm:"column:article_id"`
	StateChangedAt time.Time `gorm:"column:state_changed_at"`
}

var loadLikedHistoryPage = loadLikedHistoryPageFromDB

func GetMyLikedHistory(ctx *gin.Context) {
	viewerID, ok := requireActiveProfileViewerID(ctx)
	if !ok {
		return
	}

	limit, cursor, err := parseLikedHistoryPageQuery(ctx)
	if err != nil {
		writeLikedHistoryQueryError(ctx, err)
		return
	}

	response, err := loadLikedHistoryPage(viewerID, limit, cursor)
	if err != nil {
		writeArticleTimelineStoreError(ctx)
		return
	}
	if response.Items == nil {
		response.Items = make([]articleResponse, 0)
	}
	ctx.JSON(http.StatusOK, response)
}

func loadLikedHistoryPageFromDB(viewerID uint, limit int, cursor *likedHistoryCursor) (articlePageResponse, error) {
	if global.Db == nil {
		return articlePageResponse{}, errors.New("database is not initialized")
	}
	if viewerID == 0 || limit <= 0 {
		return articlePageResponse{}, errors.New("invalid liked history query")
	}

	var response articlePageResponse
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY").Error; err != nil {
			return err
		}

		now := time.Now().UTC()
		membershipQuery := tx.Table("article_reaction AS ar").
			Select("ar.article_id, ar.state_changed_at").
			Joins("JOIN articles ON articles.id = ar.article_id").
			Joins("JOIN users AS article_author ON article_author.id = articles.author_id AND article_author.deleted_at IS NULL").
			Where("ar.user_id = ? AND ar.liked = ?", viewerID, true).
			Scopes(func(query *gorm.DB) *gorm.DB { return publicArticleScope(query, now) })
		if cursor != nil {
			membershipQuery = membershipQuery.Where(
				"(ar.state_changed_at < ?) OR (ar.state_changed_at = ? AND ar.article_id > ?)",
				cursor.StateChangedAt,
				cursor.StateChangedAt,
				cursor.ArticleID,
			)
		}

		var membershipRows []likedHistoryMembershipRow
		if err := membershipQuery.
			Order("ar.state_changed_at DESC, ar.article_id ASC").
			Limit(limit + 1).
			Scan(&membershipRows).Error; err != nil {
			return err
		}

		hasMore := len(membershipRows) > limit
		if hasMore {
			membershipRows = membershipRows[:limit]
		}
		if len(membershipRows) == 0 {
			response = articlePageResponse{Items: make([]articleResponse, 0)}
			return nil
		}

		articleIDs := make([]uint, 0, len(membershipRows))
		for _, row := range membershipRows {
			articleIDs = append(articleIDs, row.ArticleID)
		}

		articles, err := loadArticleResponses(
			tx.Model(&models.Article{}).
				Select(publicArticleSelectColumns).
				Where("articles.id IN ?", articleIDs).
				Scopes(func(query *gorm.DB) *gorm.DB { return publicArticleScope(query, now) }),
		)
		if err != nil {
			return err
		}

		articlesByID := make(map[uint]articleResponse, len(articles))
		for _, article := range articles {
			articlesByID[article.ID] = article
		}
		items := make([]articleResponse, 0, len(membershipRows))
		for _, row := range membershipRows {
			article, ok := articlesByID[row.ArticleID]
			if !ok {
				return fmt.Errorf("liked history article %d could not be loaded", row.ArticleID)
			}
			items = append(items, article)
		}

		response = articlePageResponse{Items: items}
		if !hasMore {
			return nil
		}

		nextCursor, err := encodeLikedHistoryCursor(likedHistoryCursor{
			Version:        likedHistoryCursorVersion,
			StateChangedAt: membershipRows[len(membershipRows)-1].StateChangedAt,
			ArticleID:      membershipRows[len(membershipRows)-1].ArticleID,
		})
		if err != nil {
			return err
		}
		response.NextCursor = &nextCursor
		return nil
	})
	if err != nil {
		return articlePageResponse{}, err
	}
	return response, nil
}
