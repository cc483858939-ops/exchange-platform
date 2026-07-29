package main

import (
	"context"
	"log"
	"os"
	"strings"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/models"
)

const batchSize = 200

func main() {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("LIKE_BACKFILL_QUIESCED")), "true") {
		log.Fatal("refusing backfill: pause DB like writes and set LIKE_BACKFILL_QUIESCED=true")
	}

	config.LoadConfig()
	config.InitDB()
	config.InitRedis()

	store := likes.NewStore(global.RedisDB)
	var cursor uint
	var initialized, skipped int
	for {
		var articles []models.Article
		if err := global.Db.Select("id", "like_count", "like_sync_version").Where("id > ?", cursor).Order("id ASC").Limit(batchSize).Find(&articles).Error; err != nil {
			log.Fatalf("load articles after %d: %v", cursor, err)
		}
		if len(articles) == 0 {
			break
		}
		for _, article := range articles {
			var userIDs []uint
			if err := global.Db.Model(&models.ArticleReaction{}).
				Where("article_id = ? AND reaction = ? AND liked = ?", article.ID, models.ArticleReactionLike, true).
				Pluck("user_id", &userIDs).Error; err != nil {
				log.Fatalf("load reactions for article %d: %v", article.ID, err)
			}
			if article.LikeCount != int64(len(userIDs)) {
				log.Fatalf("refusing inconsistent article %d: like_count=%d active_reactions=%d", article.ID, article.LikeCount, len(userIDs))
			}
			created, err := store.Initialize(context.Background(), article.ID, article.LikeCount, article.LikeSyncVersion, userIDs)
			if err != nil {
				log.Fatalf("initialize Redis article %d: %v", article.ID, err)
			}
			if created {
				initialized++
			} else {
				skipped++
			}
			cursor = article.ID
		}
	}
	log.Printf("like backfill completed: initialized=%d already_ready=%d", initialized, skipped)
}
