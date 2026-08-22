package controllers

import (
	"errors"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"gorm.io/gorm"
)

func addConfiguredActivityOutboxEvent(tx *gorm.DB, envelope eventing.Envelope) error {
	if config.AppConfig == nil || config.AppConfig.Kafka.ActivityEventsTopic == "" {
		// Unit and legacy integration fixtures can exercise the existing domain
		// transaction without loading runtime Kafka configuration. Production
		// config always supplies the activity topic.
		return nil
	}
	if tx == nil {
		return errors.New("database transaction is nil")
	}
	event, err := eventing.NewOutboxEvent(config.AppConfig.Kafka, envelope)
	if err != nil {
		return err
	}
	return eventing.AddOutboxEvent(tx, event)
}
