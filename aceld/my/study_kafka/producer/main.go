package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
)

func main() {
	// 关键修改：在 Docker 容器内部，我们要访问兄弟容器 "kafka"
	// 这里的地址必须是 kafka:9092，不能是 localhost:9092
	topic := "hello-world-topic"
	partition := 0
	conn, err := kafka.DialLeader(context.Background(), "tcp", "kafka:9092", topic, partition)
	if err != nil {
		log.Fatal("连接 Kafka 失败 (请检查 docker 是否启动):", err)
	}
	defer conn.Close()

	// 设置超时，防止卡死
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	for i := 0; i < 5; i++ {
		msg := fmt.Sprintf("这是第 %d 条来自 Go 的消息", i)
		_, err = conn.WriteMessages(
			kafka.Message{Value: []byte(msg)},
		)
		if err != nil {
			log.Fatal("发送失败:", err)
		}
		fmt.Println("成功发送:", msg)
		time.Sleep(time.Second)
	}
}