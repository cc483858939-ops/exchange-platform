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
	PostID         uint      `gorm:"column:post_id"`
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
		writePostTimelineStoreError(ctx)
		return
	}
	if response.Items == nil {
		response.Items = make([]postResponse, 0)
	}
	ctx.JSON(http.StatusOK, response)
}

func loadLikedHistoryPageFromDB(viewerID uint, limit int, cursor *likedHistoryCursor) (postPageResponse, error) {
	if global.Db == nil {
		return postPageResponse{}, errors.New("database is not initialized")
	}
	if viewerID == 0 || limit <= 0 {
		return postPageResponse{}, errors.New("invalid liked history query")
	}

	var response postPageResponse
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY").Error; err != nil {
			return err
		}

		now := time.Now().UTC()
		membershipQuery := tx.Table("post_reaction AS reaction").
			Select("reaction.post_id, reaction.state_changed_at").
			Joins("JOIN posts ON posts.id = reaction.post_id").
			Where("reaction.user_id = ? AND reaction.liked = ?", viewerID, true).
			Scopes(func(query *gorm.DB) *gorm.DB { return publicPostScope(query, now) })
		if cursor != nil {
			membershipQuery = membershipQuery.Where(
				"(reaction.state_changed_at < ?) OR (reaction.state_changed_at = ? AND reaction.post_id > ?)",
				cursor.StateChangedAt,
				cursor.StateChangedAt,
				cursor.PostID,
			)
		}

		var membershipRows []likedHistoryMembershipRow
		if err := membershipQuery.
			Order("reaction.state_changed_at DESC, reaction.post_id ASC").
			Limit(limit + 1).
			Scan(&membershipRows).Error; err != nil {
			return err
		}

		hasMore := len(membershipRows) > limit
		if hasMore {
			membershipRows = membershipRows[:limit]
		}
		if len(membershipRows) == 0 {
			response = postPageResponse{Items: make([]postResponse, 0)}
			return nil
		}

		postIDs := make([]uint, 0, len(membershipRows))
		for _, row := range membershipRows {
			postIDs = append(postIDs, row.PostID)
		}

		posts, err := loadPostResponses(
			tx.Model(&models.Post{}).
				Select(publicPostSelectColumns).
				Where("posts.id IN ?", postIDs).
				Scopes(func(query *gorm.DB) *gorm.DB { return publicPostScope(query, now) }),
		)
		if err != nil {
			return err
		}

		postsByID := make(map[uint]postResponse, len(posts))
		for _, post := range posts {
			postsByID[post.ID] = post
		}
		items := make([]postResponse, 0, len(membershipRows))
		for _, row := range membershipRows {
			post, ok := postsByID[row.PostID]
			if !ok {
				return fmt.Errorf("liked history post %d could not be loaded", row.PostID)
			}
			items = append(items, post)
		}

		response = postPageResponse{Items: items}
		if !hasMore {
			return nil
		}

		nextCursor, err := encodeLikedHistoryCursor(likedHistoryCursor{
			Version:        likedHistoryCursorVersion,
			StateChangedAt: membershipRows[len(membershipRows)-1].StateChangedAt,
			PostID:         membershipRows[len(membershipRows)-1].PostID,
		})
		if err != nil {
			return err
		}
		response.NextCursor = &nextCursor
		return nil
	})
	if err != nil {
		return postPageResponse{}, err
	}
	return response, nil
}
