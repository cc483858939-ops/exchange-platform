# Kafka Introduction: Evaluation & Implementation Plan

## 1. Evaluation of Necessity
**Conclusion:** **Recommended** for long-term scalability and reliability, though current Redis implementation is functional for low load.

### Why Introduce Kafka?
1.  **Reliability & Persistence**: Redis is primarily a cache. While it supports persistence, Kafka is designed for durable event logging. If the `sync_likes` task crashes or Redis OOMs, data in Redis sets might be at risk or harder to recover than Kafka offsets.
2.  **Decoupling**: Currently, `LikeController` is coupled to the specific Redis data structures (`ArticleDirtySetKey`). With Kafka, it just emits an `ArticleLiked` event. Any service (Analytics, Push Notifications, etc.) can consume this without changing the Controller.
3.  **Scalability**: Kafka Consumer Groups allow you to easily scale the processing of likes across multiple instances of the `app` service without complex locking (which the current `manager.go` manually handles via semaphores).

### Current Readiness
- **Infrastructure**: `docker-compose.yml` already contains a configured Kafka service (KRaft mode) and Kafka UI.
- **Dependencies**: `go.mod` already includes `github.com/segmentio/kafka-go`.
- **Knowledge**: `study_kafka` directory shows basic connectivity is tested.

---

## 2. Implementation Plan (How to Introduce)

The best way to "introduce" Kafka is to migrate the existing **Like Synchronization** feature (`sync_likes.go`) from Redis-polling to Kafka-streaming.

### User Review Required
> [!IMPORTANT]
> This change replaces the Redis "Dirty Set" mechanism for syncing likes to MySQL with a Kafka Consumer.
> - **Producer**: `LikeController` will write to Kafka.
> - **Consumer**: A new background routine will read from Kafka and update MySQL.
> - **Redis**: Will still be used for *Reading* (Caching) likes to ensure high performance, but the *Write-behind* to DB will go through Kafka.

### Proposed Changes

#### 1. Configuration
Update config to include Kafka settings.

##### [MODIFY] [config.yml](file:///code/mf/Go.exchange/config/config.yml)
- Add `kafka` section (Brokers, Topic names).

##### [MODIFY] [config.go](file:///code/mf/Go.exchange/config/config.go)
- Add `KafkaConfig` struct to map the yaml.

#### 2. Global Client Initialization
Initialize the Kafka Writer (Producer) globally for reuse.

##### [NEW] [kafka.go](file:///code/mf/Go.exchange/global/kafka.go)
- Initialize `KafkaWriter` using `segmentio/kafka-go`.

#### 3. Producer Implementation
Modify the controller to produce events.

##### [MODIFY] [like_controller.go](file:///code/mf/Go.exchange/controllers/like_controller.go)
- In `LikeArticle`, send a message to `article-likes` topic containing `{"article_id": 1, "user_id": 2, "action": "like"}` (or similar).
- *Maintain Redis Increment*: Keep `RedisDB.Incr` for immediate UI feedback, but replace the `SAdd(DirtySet)` with Kafka Write.

#### 4. Consumer Implementation
Create a robust consumer group.

##### [NEW] [kafka_consumer.go](file:///code/mf/Go.exchange/tasks/kafka_consumer.go)
- Create `StartLikeConsumer(ctx)` function.
- Use `kafka.NewReader` with `GroupID: "like-sync-group"`.
- Loop: `ReadMessage` -> Accumulate Batch -> Batch Insert to MySQL -> `CommitMessages`.

##### [MODIFY] [manager.go](file:///code/mf/Go.exchange/tasks/manager.go)
- In `StartAll`, launch `go StartLikeConsumer(ctx)`.
- (Optional) Remove `staticLoop`/`dynamicLoop` related to Redis if fully replaced.

## 3. Verification Plan

### Automated Tests
- Since this involves infrastructure, unit tests are mocking-heavy. We will focus on manual integration testing first.

### Manual Verification
1.  **Start Infra**: `docker-compose up -d kafka kafka-ui`.
2.  **Topics**: Check Kafka UI (`http://localhost:8080`) to see if `article-likes` topic is created.
3.  **Trigger Like**: Call `POST /like` API.
    - Check Redis: Key should increment.
    - Check Kafka UI: Message should appear in topic.
4.  **Verify Sync**:
    - Check MySQL: `like_count` should update after a short delay (consumer processing).
