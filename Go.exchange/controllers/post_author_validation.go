package controllers

import "Go.exchange/models"

func ensurePostAuthors(posts []models.Post) error {
	for _, post := range posts {
		if _, err := publicAuthorFromPost(post); err != nil {
			return err
		}
	}
	return nil
}
