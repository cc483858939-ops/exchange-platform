package controllers

import (
	"encoding/json"
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

type postResponse struct {
	ID             uint                   `json:"id"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	PublishedAt    *time.Time             `json:"published_at"`
	Author         publicAuthorResponse   `json:"author"`
	Content        string                 `json:"content"`
	ConversationID uint                   `json:"conversation_id"`
	ReplyToPostID  *uint                  `json:"reply_to_post_id"`
	QuotePostID    *uint                  `json:"quote_post_id"`
	ReplyToPost    *postReferenceResponse `json:"reply_to_post"`
	QuotePost      *postReferenceResponse `json:"quote_post"`
	Visibility     string                 `json:"visibility"`
	Article        *postArticleResponse   `json:"article"`
	LikeCount      int64                  `json:"like_count"`
	ReplyCount     int64                  `json:"reply_count"`
	ViewCount      int64                  `json:"view_count"`
	Deleted        bool                   `json:"deleted"`
}

type postArticleResponse struct {
	Title            string     `json:"title"`
	Preview          string     `json:"preview"`
	CoverImageURL    string     `json:"cover_image_url"`
	PublicationState string     `json:"publication_state"`
	PublishedAt      *time.Time `json:"published_at"`
	ExpiredAt        *time.Time `json:"expired_at"`
}

type postReferenceArticleResponse struct {
	Title         string `json:"title"`
	Preview       string `json:"preview"`
	CoverImageURL string `json:"cover_image_url"`
}

type postReferenceResponse struct {
	ID          uint                          `json:"id"`
	Deleted     bool                          `json:"deleted"`
	Author      *publicAuthorResponse         `json:"author,omitempty"`
	Content     string                        `json:"content,omitempty"`
	PublishedAt *time.Time                    `json:"published_at,omitempty"`
	Article     *postReferenceArticleResponse `json:"article,omitempty"`
}

func (reference postReferenceResponse) MarshalJSON() ([]byte, error) {
	if reference.Deleted {
		return json.Marshal(struct {
			ID      uint `json:"id"`
			Deleted bool `json:"deleted"`
		}{ID: reference.ID, Deleted: true})
	}
	if reference.Author == nil || reference.PublishedAt == nil {
		return nil, errors.New("active post reference is incomplete")
	}
	return json.Marshal(struct {
		ID          uint                          `json:"id"`
		Author      publicAuthorResponse          `json:"author"`
		Content     string                        `json:"content"`
		PublishedAt time.Time                     `json:"published_at"`
		Article     *postReferenceArticleResponse `json:"article"`
		Deleted     bool                          `json:"deleted"`
	}{
		ID: reference.ID, Author: *reference.Author, Content: reference.Content,
		PublishedAt: reference.PublishedAt.UTC(), Article: reference.Article, Deleted: false,
	})
}

func publicAuthorFromUser(user models.User) publicAuthorResponse {
	return publicAuthorResponse{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, AvatarURL: user.AvatarURL}
}

func publicAuthorFromPost(post models.Post) (publicAuthorResponse, error) {
	if post.AuthorID == 0 || post.Author.ID == 0 || post.Author.ID != post.AuthorID {
		return publicAuthorResponse{}, errors.New("post author is missing or invalid")
	}
	return publicAuthorFromUser(post.Author), nil
}

func newPostResponse(post models.Post) (postResponse, error) {
	author, err := publicAuthorFromPost(post)
	if err != nil {
		return postResponse{}, err
	}
	publishedAt := post.CreatedAt.UTC()
	conversationID := post.ID
	if post.ConversationID != nil && *post.ConversationID != 0 {
		conversationID = *post.ConversationID
	}
	return postResponse{
		ID: post.ID, CreatedAt: post.CreatedAt.UTC(), UpdatedAt: post.UpdatedAt.UTC(),
		PublishedAt: &publishedAt, Author: author, Content: post.Content,
		ConversationID: conversationID, ReplyToPostID: post.ReplyToPostID, QuotePostID: post.QuotePostID,
		Visibility: post.Visibility, LikeCount: post.LikeCount, ReplyCount: post.ReplyCount,
		ViewCount: post.ViewCount, Deleted: false,
	}, nil
}

func postArticleResponseFromModel(article *models.PostArticle) *postArticleResponse {
	if article == nil {
		return nil
	}
	return &postArticleResponse{
		Title: article.Title, Preview: article.Preview, CoverImageURL: article.CoverImageURL,
		PublicationState: article.PublicationState, PublishedAt: article.PublishedAt,
		ExpiredAt: article.ExpiredAt,
	}
}

func postReferenceArticleFromModel(article *models.PostArticle) *postReferenceArticleResponse {
	if article == nil {
		return nil
	}
	return &postReferenceArticleResponse{
		Title: article.Title, Preview: article.Preview, CoverImageURL: article.CoverImageURL,
	}
}

func postResponseFromModel(post models.Post, article *models.PostArticle) (postResponse, error) {
	response, err := newPostResponse(post)
	if err != nil {
		return postResponse{}, err
	}
	if article != nil {
		response.Article = postArticleResponseFromModel(article)
		if article.PublishedAt != nil && !article.PublishedAt.IsZero() {
			publishedAt := article.PublishedAt.UTC()
			response.PublishedAt = &publishedAt
		}
	}
	return response, nil
}

func newPostResponses(posts []models.Post) ([]postResponse, error) {
	responses := make([]postResponse, 0, len(posts))
	for _, post := range posts {
		response, err := newPostResponse(post)
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

func hydratePostResponseAuthors(responses []postResponse) error {
	if len(responses) == 0 {
		return nil
	}
	authorIDs := make([]uint, 0, len(responses))
	seenIDs := make(map[uint]struct{}, len(responses))
	for _, response := range responses {
		if response.Author.ID == 0 {
			return errors.New("post author is missing or invalid")
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
			return fmt.Errorf("post author %d could not be found", responses[index].Author.ID)
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

func preloadPostAuthor(query *gorm.DB) *gorm.DB {
	return query.Preload("Author", func(tx *gorm.DB) *gorm.DB { return tx.Select("id, username, display_name, avatar_url") })
}

func loadPostResponses(query *gorm.DB) ([]postResponse, error) {
	if query == nil {
		return nil, errors.New("database query is nil")
	}
	var posts []models.Post
	if err := preloadPostAuthor(query).Find(&posts).Error; err != nil {
		return nil, err
	}
	// Keep extension hydration on the same transaction/connection as the Post
	// query. This matters for read-only repeatable-read pages and for newly
	// committed Post + PostArticle pairs.
	articleDB := query.Session(&gorm.Session{NewDB: true})
	articles, err := loadPostArticlesFromDB(articleDB, posts)
	if err != nil {
		return nil, err
	}
	responses := make([]postResponse, 0, len(posts))
	referenceNow := time.Now().UTC()
	for _, post := range posts {
		response, err := postResponseFromModel(post, articles[post.ID])
		if err != nil {
			return nil, err
		}
		if err := hydratePostResponseReferencesFromDB(articleDB, &response, referenceNow); err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}
	return responses, nil
}
