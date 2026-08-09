package controllers

import (
	"testing"

	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func createArticleIntegrationAuthor(t *testing.T, db *gorm.DB) models.User {
	t.Helper()
	user := models.User{Username: "article-author-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&user)
	})
	return user
}
