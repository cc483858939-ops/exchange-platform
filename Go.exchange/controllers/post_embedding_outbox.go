package controllers

import (
	"errors"
	"strings"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"gorm.io/gorm"
)

func addConfiguredPostEmbeddingOutboxEvent(tx *gorm.DB, envelope eventing.Envelope) error {
	if tx == nil {
		return errors.New("database transaction is nil")
	}
	if config.AppConfig == nil {
		return errors.New("application config is not initialized")
	}
	if strings.TrimSpace(config.AppConfig.Kafka.PostEmbeddingTopic) == "" {
		return errors.New("Kafka post embedding topic is not configured")
	}
	event, err := eventing.NewOutboxEvent(config.AppConfig.Kafka, envelope)
	if err != nil {
		return err
	}
	return eventing.AddOutboxEvent(tx, event)
}
