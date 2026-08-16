package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

const (
	generatedCoverObjectPrefix = "article-covers/generated/"
	maxGeneratedCoverSize      = 5 << 20
)

type coverManifest struct {
	Covers []coverManifestEntry `json:"covers"`
}

type coverManifestEntry struct {
	ArticleID uint   `json:"article_id"`
	ImagePath string `json:"image_path"`
}

func main() {
	manifestPath := flag.String("manifest", "assets/generated-covers/backfill-manifest.json", "path to the approved cover manifest")
	dryRun := flag.Bool("dry-run", false, "validate the manifest and eligible articles without uploading or writing")
	flag.Parse()

	manifest, err := loadManifest(*manifestPath)
	if err != nil {
		log.Fatal(err)
	}

	config.InitConfig()
	ctx := context.Background()
	updated, skipped := 0, 0
	for _, entry := range manifest.Covers {
		changed, err := backfillArticleCover(ctx, entry, *dryRun)
		if err != nil {
			log.Fatalf("article %d: %v", entry.ArticleID, err)
		}
		if changed {
			updated++
		} else {
			skipped++
		}
	}

	log.Printf("article cover backfill completed: updated=%d skipped=%d dry_run=%t", updated, skipped, *dryRun)
}

func loadManifest(path string) (coverManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return coverManifest{}, fmt.Errorf("open manifest: %w", err)
	}
	defer file.Close()

	var manifest coverManifest
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return coverManifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if len(manifest.Covers) == 0 {
		return coverManifest{}, errors.New("manifest has no covers")
	}

	seen := make(map[uint]struct{}, len(manifest.Covers))
	for _, entry := range manifest.Covers {
		if entry.ArticleID == 0 || strings.TrimSpace(entry.ImagePath) == "" {
			return coverManifest{}, errors.New("every manifest entry requires article_id and image_path")
		}
		if _, exists := seen[entry.ArticleID]; exists {
			return coverManifest{}, fmt.Errorf("manifest repeats article %d", entry.ArticleID)
		}
		seen[entry.ArticleID] = struct{}{}
	}
	return manifest, nil
}

func backfillArticleCover(ctx context.Context, entry coverManifestEntry, dryRun bool) (bool, error) {
	var article models.Article
	if err := global.Db.Select("id", "title", "preview", "cover_image_url").First(&article, entry.ArticleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, fmt.Errorf("article does not exist")
		}
		return false, fmt.Errorf("load article: %w", err)
	}
	if strings.TrimSpace(article.CoverImageURL) != "" {
		log.Printf("article %d already has a cover; skipping", article.ID)
		return false, nil
	}
	if strings.HasPrefix(article.Title, "LT-QPS-") || strings.EqualFold(strings.TrimSpace(article.Preview), "load-test") {
		return false, errors.New("refusing to backfill a load-test article")
	}

	contentType, extension, err := generatedCoverContentType(entry.ImagePath)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(entry.ImagePath)
	if err != nil {
		return false, fmt.Errorf("stat image: %w", err)
	}
	if info.Size() <= 0 || info.Size() > maxGeneratedCoverSize {
		return false, fmt.Errorf("image size %d is outside the allowed range", info.Size())
	}
	if dryRun {
		log.Printf("article %d eligible for cover backfill from %s", article.ID, entry.ImagePath)
		return true, nil
	}

	image, err := os.Open(entry.ImagePath)
	if err != nil {
		return false, fmt.Errorf("open image: %w", err)
	}
	defer image.Close()

	objectKey := fmt.Sprintf("%sarticle-%d-%s%s", generatedCoverObjectPrefix, article.ID, uuid.NewString(), extension)
	if _, err := global.MinioClient.PutObject(ctx, config.StorageBucket(), objectKey, image, info.Size(), minio.PutObjectOptions{ContentType: contentType}); err != nil {
		return false, fmt.Errorf("upload cover: %w", err)
	}

	coverURL := "/api/files/" + objectKey
	result := global.Db.Model(&models.Article{}).
		Where("id = ? AND (cover_image_url IS NULL OR btrim(cover_image_url) = '')", article.ID).
		Update("cover_image_url", coverURL)
	if result.Error != nil {
		_ = global.MinioClient.RemoveObject(ctx, config.StorageBucket(), objectKey, minio.RemoveObjectOptions{})
		return false, fmt.Errorf("write cover URL: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		_ = global.MinioClient.RemoveObject(ctx, config.StorageBucket(), objectKey, minio.RemoveObjectOptions{})
		log.Printf("article %d gained a cover while this backfill ran; skipping", article.ID)
		return false, nil
	}

	log.Printf("article %d cover backfilled as %s", article.ID, coverURL)
	return true, nil
}

func generatedCoverContentType(path string) (string, string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png", ".png", nil
	case ".jpg", ".jpeg":
		return "image/jpeg", ".jpg", nil
	case ".webp":
		return "image/webp", ".webp", nil
	default:
		return "", "", errors.New("generated cover must be a png, jpeg, or webp image")
	}
}
