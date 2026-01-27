package main

import (
	"context"
	"fmt"
	"log"

	"github.com/segmentio/kafka-go"
)

func main() {
	// 同样，地址必须是 kafka:9092
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:   []string{"kafka:9092"},
		Topic:     "hello-world-topic",
		Partition: 0,
		MinBytes:  10e3, // 10KB
		MaxBytes:  10e6, // 10MB
	})
	defer r.Close()

	fmt.Println("消费者已启动，正在等待消息...")

	for {
		m, err := r.ReadMessage(context.Background())
		if err != nil {
			log.Fatal("读取失败:", err)
			break
		}
		fmt.Printf("收到消息: %s (偏移量: %d)\n", string(m.Value), m.Offset)
	}
}