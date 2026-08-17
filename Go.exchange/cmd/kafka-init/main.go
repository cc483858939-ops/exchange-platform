package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"Go.exchange/config"
	"Go.exchange/eventing"
)

func main() {
	config.LoadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	specs, err := eventing.RequiredKafkaTopics(config.AppConfig.Kafka)
	if err != nil {
		log.Printf("Kafka topic configuration is invalid: %v", err)
		os.Exit(1)
	}
	for _, spec := range specs {
		log.Printf("Kafka topic required: name=%s partitions=%d replication_factor=%d", spec.Name, spec.Partitions, spec.ReplicationFactor)
	}
	if err := eventing.EnsureKafkaTopics(ctx, config.AppConfig.Kafka); err != nil {
		log.Printf("Kafka topic provisioning failed: %v", err)
		os.Exit(1)
	}
	log.Printf("Kafka topic provisioning completed: %d topics ready", len(specs))
}
