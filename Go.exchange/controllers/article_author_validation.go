package controllers

import "Go.exchange/models"

func ensureArticleAuthors(articles []models.Article) error {
	for _, article := range articles {
		if _, err := publicAuthorFromArticle(article); err != nil {
			return err
		}
	}
	return nil
}
