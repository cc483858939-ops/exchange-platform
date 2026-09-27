package main

import (
	"context"
	"io"
	"log"
	"sync"

	"Go.exchange/auth"
	"Go.exchange/config"
	"Go.exchange/core"
	"Go.exchange/eventing"
	"Go.exchange/global"

	"github.com/gin-gonic/gin"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := config.ValidateAPIEventingConfig(cfg); err != nil {
		return err
	}

	db, err := config.OpenAPIDatabase(cfg)
	if err != nil {
		return err
	}
	global.Db = db
	global.APIDb = db
	global.WorkerDb = nil
	defer func() {
		if err := config.CloseDatabase(db); err != nil {
			log.Printf("close API database: %v", err)
		}
	}()

	redisClient, err := config.NewRedisClient()
	if err != nil {
		return err
	}
	global.RedisDB = redisClient
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Printf("close API Redis client: %v", err)
		}
	}()

	storageClient, err := config.NewStorageClient()
	if err != nil {
		return err
	}
	global.MinioClient = storageClient

	tokenConfig, err := auth.LoadConfigFromEnv()
	if err != nil {
		return err
	}
	refreshStore, err := auth.NewRedisRefreshStore(redisClient)
	if err != nil {
		return err
	}
	tokens, err := auth.NewManager(tokenConfig, refreshStore)
	if err != nil {
		return err
	}
	log.Printf("JWT signer initialized with kid=%s", tokenConfig.ActiveKID)

	publisher, err := eventing.NewKafkaPublisher(cfg.Kafka)
	if err != nil {
		return err
	}
	defer func() {
		if err := publisher.Close(); err != nil {
			log.Printf("close API Kafka publisher: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waitGroup sync.WaitGroup
	apiRuntime, err := core.StartHttpServer(tokens, publisher)
	if err != nil {
		return err
	}
	core.WaitForShutdown(ctx, cancel, apiRuntime, &waitGroup)
	return nil
}
