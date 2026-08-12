package controllers

import (
	"errors"
	"fmt"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

type publicAuthorResponse struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

type publicUserResponse struct {
	ID          uint      `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Bio         string    `json:"bio"`
	AvatarURL   string    `json:"avatar_url"`
	CreatedAt   time.Time `json:"created_at"`
}

// articleResponse preserves existing article field names while omitting internal identity and soft-delete fields.
type articleResponse struct {
	ID               uint                 `json:"ID"`
	CreatedAt        time.Time            `json:"CreatedAt"`
	UpdatedAt        time.Time            `json:"UpdatedAt"`
	Title            string               `json:"title"`
	Content          string               `json:"content"`
	Preview          string               `json:"preview"`
	CoverImageURL    string               `json:"cover_image_url"`
	Summary          string               `json:"summary"`
	Tags             []string             `json:"tags"`
	Category         string               `json:"category"`
	PublicationState string               `json:"publication_state"`
	AnalysisState    string               `json:"analysis_state"`
	AnalysisVersion  string               `json:"analysis_version"`
	PublishedAt      *time.Time           `json:"published_at"`
	ExpiredAt        *time.Time           `json:"expired_at"`
	LikeCount        int64                `json:"like_count"`
	CommentCount     int64                `json:"comment_count"`
	LikeSyncVersion  int64                `json:"like_sync_version"`
	Author           publicAuthorResponse `json:"author"`
}

func publicAuthorFromUser(user models.User) publicAuthorResponse {
	return publicAuthorResponse{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		AvatarURL:   user.AvatarURL,
	}
}

func publicAuthorFromArticle(article models.Article) (publicAuthorResponse, error) {
	if article.AuthorID == 0 || article.Author.ID == 0 || article.Author.ID != article.AuthorID {
		return publicAuthorResponse{}, errors.New("article author is missing or invalid")
	}
	return publicAuthorFromUser(article.Author), nil
}

func newArticleResponse(article models.Article) (articleResponse, error) {
	author, err := publicAuthorFromArticle(article)
	if err != nil {
		return articleResponse{}, err
	}
	return articleResponse{
		ID: article.ID, CreatedAt: article.CreatedAt, UpdatedAt: article.UpdatedAt,
		Title: article.Title, Content: article.Content, Preview: article.Preview,
		CoverImageURL: article.CoverImageURL, Summary: article.Summary, Tags: article.Tags,
		Category: article.Category, PublicationState: article.PublicationState,
		AnalysisState: article.AnalysisState, AnalysisVersion: article.AnalysisVersion,
		PublishedAt: article.PublishedAt, ExpiredAt: article.ExpiredAt,
		LikeCount: article.LikeCount, CommentCount: article.CommentCount, LikeSyncVersion: article.LikeSyncVersion,
		Author: author,
	}, nil
}

func newArticleResponses(articles []models.Article) ([]articleResponse, error) {
	responses := make([]articleResponse, 0, len(articles))
	for _, article := range articles {
		response, err := newArticleResponse(article)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}
	return responses, nil
}

func loadPublicAuthorByID(id uint) (publicAuthorResponse, error) {
	if id == 0 || global.Db == nil {
		return publicAuthorResponse{}, errors.New("database is not initialized")
	}
	var user models.User
	if err := global.Db.Select("id, username, display_name, avatar_url").First(&user, id).Error; err != nil {
		return publicAuthorResponse{}, err
	}
	return publicAuthorFromUser(user), nil
}

var loadPublicAuthorsByIDs = loadPublicAuthorsByIDsFromDB

func loadPublicAuthorsByIDsFromDB(ids []uint) (map[uint]publicAuthorResponse, error) {
	uniqueIDs := make([]uint, 0, len(ids))
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	authors := make(map[uint]publicAuthorResponse, len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		return authors, nil
	}
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}

	var users []models.User
	if err := global.Db.Select("id, username, display_name, avatar_url").Where("id IN ?", uniqueIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, user := range users {
		authors[user.ID] = publicAuthorFromUser(user)
	}
	return authors, nil
}

func hydrateArticleResponseAuthors(responses []articleResponse) error {
	if len(responses) == 0 {
		return nil
	}
	authorIDs := make([]uint, 0, len(responses))
	seenIDs := make(map[uint]struct{}, len(responses))
	for _, response := range responses {
		if response.Author.ID == 0 {
			return errors.New("article author is missing or invalid")
		}
		if _, exists := seenIDs[response.Author.ID]; exists {
			continue
		}
		seenIDs[response.Author.ID] = struct{}{}
		authorIDs = append(authorIDs, response.Author.ID)
	}
	authors, err := loadPublicAuthorsByIDs(authorIDs)
	if err != nil {
		return err
	}
	for index := range responses {
		author, ok := authors[responses[index].Author.ID]
		if !ok {
			return fmt.Errorf("article author %d could not be found", responses[index].Author.ID)
		}
		responses[index].Author = author
	}
	return nil
}

func loadPublicUserByID(id uint) (publicUserResponse, error) {
	if id == 0 || global.Db == nil {
		return publicUserResponse{}, errors.New("database is not initialized")
	}
	var user models.User
	if err := global.Db.Select("id, username, display_name, bio, avatar_url, created_at").First(&user, id).Error; err != nil {
		return publicUserResponse{}, err
	}
	return publicUserResponse{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Bio: user.Bio, AvatarURL: user.AvatarURL, CreatedAt: user.CreatedAt}, nil
}

func preloadArticleAuthor(query *gorm.DB) *gorm.DB {
	return query.Preload("Author", func(tx *gorm.DB) *gorm.DB {
		return tx.Select("id, username, display_name, avatar_url")
	})
}

func loadArticleResponses(query *gorm.DB) ([]articleResponse, error) {
	var articles []models.Article
	if err := preloadArticleAuthor(query).Find(&articles).Error; err != nil {
		return nil, err
	}
	return newArticleResponses(articles)
}
