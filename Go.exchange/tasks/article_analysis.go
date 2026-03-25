package tasks

import (
	"context"
	"errors"
	"log"
	"strconv"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/controllers"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

const (
	// 先用小并发保守跑，避免第一次接入就把模型接口打满。
	articleAnalysisWorkerCount = 2
	// 每次只取 1 个文章 ID，先把状态流转和失败处理做稳。
	articleAnalysisBatchSize = 1
)

var newArticleAnalyzer = func() (ArticleAnalyzer, error) {
	return NewEINOArticleAnalysisAgent(config.AppConfig.AI)
}

var loadArticleForAnalysis = func(id uint) (models.Article, error) {
	var article models.Article
	err := global.Db.Select("id", "title", "preview", "content").First(&article, id).Error
	return article, err
}

var updateArticleAnalysisStatus = func(id uint, status string) error {
	return global.Db.Model(&models.Article{}).Where("id = ?", id).Update("status", status).Error
}

var saveArticleAnalysisResult = func(id uint, result ArticleAnalysisResult) error {
	return global.Db.Model(&models.Article{}).Where("id = ?", id).Updates(map[string]interface{}{
		"summary":  result.Summary,
		"tags":     result.Tags,
		"category": result.Category,
		"status":   consts.ArticleStatusCompleted,
	}).Error
}

var ackArticleAnalysisTask = func(articleID uint) error {
	return global.RedisDB.SRem(consts.ArticleAnalysisProcessingSetKey, articleID).Err()
}

var invalidateArticleDetailCache = controllers.InvalidateArticleDetailCacheByID

func startArticleAnalysisWorkers(ctx context.Context, wg *sync.WaitGroup) {
	recoverOrphanedArticleAnalysisData()

	for i := 0; i < articleAnalysisWorkerCount; i++ {
		wg.Add(1)
		go articleAnalysisLoop(ctx, wg)
	}
	log.Printf("[Task] started %d article analysis workers", articleAnalysisWorkerCount)
}

func recoverOrphanedArticleAnalysisData() {
	// 服务异常退出时，processing_set 里可能残留未 ACK 的文章 ID。
	// 启动时把它们并回 dirty_set，保证这些任务还能被重新消费。
	count, err := global.RedisDB.SCard(consts.ArticleAnalysisProcessingSetKey).Result()
	if err != nil {
		log.Printf("[Task] [ArticleAnalysis Recover] failed to read processing set: %v", err)
		return
	}
	if count == 0 {
		return
	}

	err = global.RedisDB.SUnionStore(
		consts.ArticleAnalysisDirtySetKey,
		consts.ArticleAnalysisDirtySetKey,
		consts.ArticleAnalysisProcessingSetKey,
	).Err()
	if err != nil {
		log.Printf("[Task] [ArticleAnalysis Recover] failed to merge processing set back to dirty set: %v", err)
		return
	}

	global.RedisDB.Del(consts.ArticleAnalysisProcessingSetKey)
	log.Printf("[Task] [ArticleAnalysis Recover] moved %d orphaned ids back to dirty set", count)
}

func articleAnalysisLoop(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	// 配置缺失时直接停掉当前 worker，避免把整个服务启动打挂。
	analyzer, err := newArticleAnalyzer()
	if err != nil {
		log.Printf("[Task] article analysis worker disabled: %v", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ids, err := fetchArticleAnalysisBatch()
		if err != nil {
			log.Printf("[Task] fetch article analysis batch failed: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if len(ids) == 0 {
			time.Sleep(time.Second)
			continue
		}

		for _, id := range ids {
			processArticleAnalysisTask(ctx, analyzer, id)
		}
	}
}

func fetchArticleAnalysisBatch() ([]uint, error) {
	val, err := global.RedisDB.Eval(
		consts.FetchArticleAnalysisBatchScript,
		[]string{consts.ArticleAnalysisDirtySetKey, consts.ArticleAnalysisProcessingSetKey},
		articleAnalysisBatchSize,
	).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}

	rawList, ok := val.([]interface{})
	if !ok || len(rawList) == 0 {
		return nil, nil
	}

	ids := make([]uint, 0, len(rawList))
	for _, rawID := range rawList {
		idStr := strconv.FormatInt(toInt64(rawID), 10)
		idUint64, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			log.Printf("[Task] skip invalid article analysis id %v: %v", rawID, err)
			continue
		}
		ids = append(ids, uint(idUint64))
	}
	return ids, nil
}

func processArticleAnalysisTask(ctx context.Context, analyzer ArticleAnalyzer, articleID uint) {
	defer func() {
		// v1 不做自动重试，所以成功或失败都要 ACK，避免坏任务在 Redis 里死循环。
		if err := ackArticleAnalysisTask(articleID); err != nil {
			log.Printf("[Task] failed to ACK article analysis task %d: %v", articleID, err)
		}
	}()

	// 状态刚进入 processing 时就删一次详情缓存，让读请求尽快看到最新状态。
	if err := updateArticleAnalysisStatus(articleID, consts.ArticleStatusProcessing); err != nil {
		log.Printf("[Task] failed to mark article %d as processing: %v", articleID, err)
		return
	}
	invalidateArticleDetailCacheBestEffort(articleID)

	article, err := loadArticleForAnalysis(articleID)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("[Task] failed to load article %d for analysis: %v", articleID, err)
		}
		markArticleAnalysisFailed(articleID)
		return
	}

	result, err := analyzer.Analyze(ctx, article)
	if err != nil {
		log.Printf("[Task] failed to analyze article %d: %v", articleID, err)
		markArticleAnalysisFailed(articleID)
		return
	}

	if err := saveArticleAnalysisResult(articleID, result); err != nil {
		log.Printf("[Task] failed to save article analysis result for %d: %v", articleID, err)
		markArticleAnalysisFailed(articleID)
		return
	}

	// AI 结果回写成功后再删一次详情缓存，让读请求拿到 summary/tags/category。
	invalidateArticleDetailCacheBestEffort(articleID)
}

func markArticleAnalysisFailed(articleID uint) {
	if err := updateArticleAnalysisStatus(articleID, consts.ArticleStatusFailed); err != nil {
		log.Printf("[Task] failed to mark article %d as failed: %v", articleID, err)
	}
	invalidateArticleDetailCacheBestEffort(articleID)
}

func invalidateArticleDetailCacheBestEffort(articleID uint) {
	if err := invalidateArticleDetailCache(articleID); err != nil {
		log.Printf("[Task] failed to invalidate article detail cache for %d: %v", articleID, err)
	}
}

func toInt64(value interface{}) int64 {
	// Lua 返回值在不同路径下可能是 int、int64、uint 或 string，这里统一转成 int64。
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case uint64:
		return int64(v)
	case uint:
		return int64(v)
	case string:
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}
