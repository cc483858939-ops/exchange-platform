package controllers

import (
	"errors"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

type publicAuthorResponse struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
}

type publicUserResponse struct {
	ID        uint      `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
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
	LikeSyncVersion  int64                `json:"like_sync_version"`
	Author           publicAuthorResponse `json:"author"`
}

func publicAuthorFromArticle(article models.Article) (publicAuthorResponse, error) {
	if article.AuthorID == 0 || article.Author.ID == 0 || article.Author.ID != article.AuthorID {
		return publicAuthorResponse{}, errors.New("article author is missing or invalid")
	}
	return publicAuthorResponse{ID: article.Author.ID, Username: article.Author.Username}, nil
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
		LikeCount: article.LikeCount, LikeSyncVersion: article.LikeSyncVersion,
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
	if err := global.Db.Select("id, username").First(&user, id).Error; err != nil {
		return publicAuthorResponse{}, err
	}
	return publicAuthorResponse{ID: user.ID, Username: user.Username}, nil
}

func loadPublicUserByID(id uint) (publicUserResponse, error) {
	if id == 0 || global.Db == nil {
		return publicUserResponse{}, errors.New("database is not initialized")
	}
	var user models.User
	if err := global.Db.Select("id, username, created_at").First(&user, id).Error; err != nil {
		return publicUserResponse{}, err
	}
	return publicUserResponse{ID: user.ID, Username: user.Username, CreatedAt: user.CreatedAt}, nil
}

func preloadArticleAuthor(query *gorm.DB) *gorm.DB {
	return query.Preload("Author", func(tx *gorm.DB) *gorm.DB {
		return tx.Select("id, username")
	})
}

func loadArticleResponses(query *gorm.DB) ([]articleResponse, error) {
	var articles []models.Article
	if err := preloadArticleAuthor(query).Find(&articles).Error; err != nil {
		return nil, err
	}
	return newArticleResponses(articles)
}
