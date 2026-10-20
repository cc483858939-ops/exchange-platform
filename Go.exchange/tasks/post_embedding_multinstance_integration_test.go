package tasks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type postEmbeddingGenerationNotice struct {
	clientID   string
	generation int32
	partitions []int
}

type postEmbeddingProcessedMessage struct {
	clientID  string
	partition int
	offset    int64
	value     string
}

type postEmbeddingKafkaTestMember struct {
	clientID string
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
	err      error
}

func (m *postEmbeddingKafkaTestMember) stop() error {
	m.once.Do(func() {
		m.cancel()
		<-m.done
	})
	return m.err
}

// This test creates a uniquely named topic and group, so require an explicit
// disposable-cluster acknowledgement before allowing it to mutate Kafka.
func TestPostEmbeddingMultipleMembers503HoldRecoveryAndAssignmentTransferIntegration(t *testing.T) {
	if os.Getenv("KAFKA_POST_EMBEDDING_E2E_CONFIRM") != "dedicated-disposable" {
		t.Skip("set KAFKA_POST_EMBEDDING_E2E_CONFIRM=dedicated-disposable for disposable Kafka integration")
	}
	brokers := normalizedPostEmbeddingBrokers(strings.Split(os.Getenv("KAFKA_BROKERS"), ","))
	if len(brokers) == 0 {
		t.Skip("set KAFKA_BROKERS for real Post Embedding Consumer Group integration")
	}

	const partitionCount = 6
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	topic := "goexchange.post.embedding.specv2." + suffix
	groupID := "post-embedding-specv2-" + suffix
	kafkaConfig := config.KafkaConfig{Brokers: brokers, PostEmbeddingTopic: topic, PostEmbeddingGroupID: groupID}
	createPostEmbeddingIntegrationTopic(t, brokers[0], topic, partitionCount)
	t.Cleanup(func() { deletePostEmbeddingIntegrationTopic(t, brokers[0], topic) })

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	failProvider := atomic.Bool{}
	partitionHold := atomic.Bool{}
	blockPartitionRetry := atomic.Bool{}
	blockMemberA := atomic.Bool{}
	providerFailures := make(chan string, 256)
	partitionHeld := make(chan int, 32)
	heldRetryStarted := make(chan string, 32)
	releasePartitionHold := make(chan struct{})
	processed := make(chan postEmbeddingProcessedMessage, 512)
	generationNotices := make(chan postEmbeddingGenerationNotice, 128)
	failoverBlocked := make(chan struct{}, 1)

	clientA := "post-embedding-e2e-a-" + suffix
	clientB := "post-embedding-e2e-b-" + suffix
	startMember := func(clientID string) *postEmbeddingKafkaTestMember {
		t.Helper()
		group, err := kafka.NewConsumerGroup(kafka.ConsumerGroupConfig{
			ID: groupID, Brokers: brokers, Topics: []string{topic},
			Dialer: &kafka.Dialer{ClientID: clientID}, StartOffset: kafka.FirstOffset,
			Timeout: 5 * time.Second, SessionTimeout: 9 * time.Second, HeartbeatInterval: 2 * time.Second,
		})
		if err != nil {
			t.Fatalf("create real Kafka consumer group member %s: %v", clientID, err)
		}
		groupAdapter := &kafkaPostEmbeddingConsumerGroup{group: group}
		memberCtx, cancel := context.WithCancel(rootCtx)
		member := &postEmbeddingKafkaTestMember{clientID: clientID, cancel: cancel, done: make(chan struct{})}
		gate := newPostEmbeddingProviderGate(providerRetryRuntime{})
		runtime := newProductionPostEmbeddingGroupRuntime(kafkaConfig, clientID, 1, 0, nil, gate, providerRetryRuntime{})
		runtime.retryRuntime.Sleep = func(ctx context.Context, delay time.Duration) error {
			if blockPartitionRetry.Load() {
				select {
				case heldRetryStarted <- clientID:
				default:
				}
				select {
				case <-releasePartitionHold:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return waitForPostEmbeddingProviderDelay(ctx, delay)
		}
		runtime.processBatch = func(ctx context.Context, messages []kafka.Message) error {
			for _, message := range messages {
				value := string(message.Value)
				if value == "failover-pending" && clientID == clientA && blockMemberA.Load() {
					select {
					case failoverBlocked <- struct{}{}:
					default:
					}
					<-ctx.Done()
					return ctx.Err()
				}
				if value == "partition-held" && partitionHold.Load() {
					permit, err := gate.acquire(ctx)
					if err != nil {
						return err
					}
					decision := ProviderDecision{Action: ProviderActionPause, Reason: providerReasonConfig, HTTPStatus: 413}
					gate.providerRequestFailed(permit, decision, postEmbeddingFailureScopePartition)
					select {
					case partitionHeld <- message.Partition:
					default:
					}
					return &postEmbeddingProviderFailure{decision: decision, cause: errors.New("injected single-message HTTP 413")}
				}
			}
			permit, err := gate.acquire(ctx)
			if err != nil {
				return err
			}
			if failProvider.Load() {
				decision := ProviderDecision{
					Action: ProviderActionRetry, Reason: providerReasonServer, HTTPStatus: 503,
					RetryAfter: 12 * time.Second, RetryAfterProvided: true, RetryAfterValid: true,
				}
				gate.providerRequestFailed(permit, decision, postEmbeddingFailureScopeSharedOrUnknown)
				select {
				case providerFailures <- clientID:
				default:
				}
				return retryableKafkaError(kafkaFailureCodeProviderRetryable, &postEmbeddingProviderFailure{decision: decision, cause: errors.New("HTTP 503")})
			}
			gate.providerRequestSucceeded(permit)
			for _, message := range messages {
				select {
				case processed <- postEmbeddingProcessedMessage{clientID: clientID, partition: message.Partition, offset: message.Offset, value: string(message.Value)}:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}
		runtime.onGenerationStart = func(generationID int32, partitions []int) {
			copyOfPartitions := append([]int(nil), partitions...)
			select {
			case generationNotices <- postEmbeddingGenerationNotice{clientID: clientID, generation: generationID, partitions: copyOfPartitions}:
			default:
			}
		}
		go func() {
			runErr := runPostEmbeddingKafkaGroup(memberCtx, groupAdapter, runtime)
			closeErr := groupAdapter.Close()
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				member.err = runErr
			} else if closeErr != nil {
				member.err = closeErr
			}
			close(member.done)
		}()
		t.Cleanup(func() {
			if err := member.stop(); err != nil {
				t.Errorf("stop Kafka member %s: %v", clientID, err)
			}
		})
		return member
	}

	memberA := startMember(clientA)
	memberB := startMember(clientB)
	waitForPostEmbeddingGroupMembers(t, kafkaConfig, 2, 30*time.Second)
	latestGeneration := waitForPostEmbeddingGenerationNotices(t, generationNotices, clientA, clientB)
	settlePostEmbeddingGenerationNotices(generationNotices, latestGeneration, clientA, clientB)
	if len(latestGeneration[clientA].partitions) == 0 || len(latestGeneration[clientB].partitions) == 0 {
		t.Fatalf("expected both active group members to hold partitions: A=%v B=%v", latestGeneration[clientA].partitions, latestGeneration[clientB].partitions)
	}
	drainPostEmbeddingGenerationNotices(generationNotices)

	failProvider.Store(true)
	for partition := 0; partition < partitionCount; partition++ {
		writePostEmbeddingIntegrationMessage(t, brokers[0], topic, partition, fmt.Sprintf("initial-p%d", partition))
	}
	failedMembers := make(map[string]bool)
	for len(failedMembers) < 2 {
		select {
		case clientID := <-providerFailures:
			failedMembers[clientID] = true
		case <-time.After(8 * time.Second):
			t.Fatalf("Provider 503 was not observed on both members; failures=%v", failedMembers)
		}
	}
	// The configured SessionTimeout is 9 seconds. A 12 second provider Retry-After
	// keeps this broker-side assignment in the same generation beyond that timeout.
	for held := time.NewTimer(10 * time.Second); ; {
		select {
		case notice := <-generationNotices:
			t.Fatalf("Provider pause changed Generation before recovery: %+v", notice)
		case <-held.C:
			goto providerHoldVerified
		case <-rootCtx.Done():
			held.Stop()
			t.Fatal("test context canceled during Provider hold")
		}
	}
providerHoldVerified:
	description := waitForPostEmbeddingGroupMembers(t, kafkaConfig, 2, 5*time.Second)
	if description.State != "Stable" {
		t.Fatalf("group state during Provider hold=%q, want Stable", description.State)
	}
	failProvider.Store(false)
	wanted := make([]string, partitionCount)
	for partition := 0; partition < partitionCount; partition++ {
		wanted[partition] = fmt.Sprintf("initial-p%d", partition)
	}
	awaitPostEmbeddingProcessedValues(t, processed, wanted, 20*time.Second)
	waitForPostEmbeddingLagZero(t, kafkaConfig, 15*time.Second)
	if notice, ok := receivePostEmbeddingGenerationNotice(generationNotices); ok {
		t.Fatalf("unexpected Generation change after Provider recovery: %+v", notice)
	}

	partitionHold.Store(true)
	blockPartitionRetry.Store(true)
	writePostEmbeddingIntegrationMessage(t, brokers[0], topic, 0, "partition-held")
	select {
	case heldPartition := <-partitionHeld:
		if heldPartition != 0 {
			t.Fatalf("held partition=%d want 0", heldPartition)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("partition-local bad message did not enter hold")
	}
	select {
	case <-heldRetryStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("partition-local retry did not enter its bounded hold wait")
	}
	writePostEmbeddingIntegrationMessage(t, brokers[0], topic, 1, "other-partition-progress")
	awaitPostEmbeddingProcessedValues(t, processed, []string{"other-partition-progress"}, 8*time.Second)
	for held := time.NewTimer(10 * time.Second); ; {
		select {
		case notice := <-generationNotices:
			t.Fatalf("single-partition 413 hold changed Generation before retry: %+v", notice)
		case <-held.C:
			goto partitionHoldVerified
		case <-rootCtx.Done():
			held.Stop()
			t.Fatal("test context canceled during partition-local hold")
		}
	}
partitionHoldVerified:
	description = waitForPostEmbeddingGroupMembers(t, kafkaConfig, 2, 5*time.Second)
	if description.State != "Stable" {
		t.Fatalf("group state during partition-local 413 hold=%q, want Stable", description.State)
	}
	partitionHold.Store(false)
	blockPartitionRetry.Store(false)
	close(releasePartitionHold)
	awaitPostEmbeddingProcessedValues(t, processed, []string{"partition-held"}, 8*time.Second)
	waitForPostEmbeddingLagZero(t, kafkaConfig, 10*time.Second)

	assignedA := latestGeneration[clientA].partitions
	if len(assignedA) == 0 {
		t.Fatal("first member has no partition for assignment-transfer check")
	}
	transferPartition := assignedA[0]
	blockMemberA.Store(true)
	writePostEmbeddingIntegrationMessage(t, brokers[0], topic, transferPartition, "failover-pending")
	select {
	case <-failoverBlocked:
	case <-time.After(8 * time.Second):
		t.Fatal("first member did not hold the uncommitted transfer message")
	}
	if err := memberA.stop(); err != nil {
		t.Fatalf("first Kafka member exit: %v", err)
	}
	awaitPostEmbeddingProcessedValues(t, processed, []string{"failover-pending"}, 20*time.Second)
	description = waitForPostEmbeddingGroupMembers(t, kafkaConfig, 1, 15*time.Second)
	if description.State != "Stable" || len(description.Members) != 1 || description.Members[0].ClientID != clientB {
		t.Fatalf("remaining group member after transfer=%+v", description)
	}
	waitForPostEmbeddingLagZero(t, kafkaConfig, 15*time.Second)
	if err := memberB.stop(); err != nil {
		t.Fatalf("remaining Kafka member exit: %v", err)
	}
}

func createPostEmbeddingIntegrationTopic(t *testing.T, broker, topic string, partitions int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := kafka.DialContext(ctx, "tcp", broker)
	if err != nil {
		t.Fatalf("connect to disposable Kafka broker: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.CreateTopics(kafka.TopicConfig{Topic: topic, NumPartitions: partitions, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create temporary Post Embedding topic: %v", err)
	}
}

func deletePostEmbeddingIntegrationTopic(t *testing.T, broker, topic string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := kafka.DialContext(ctx, "tcp", broker)
	if err != nil {
		t.Errorf("connect to delete temporary Post Embedding topic: %v", err)
		return
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Errorf("set temporary topic cleanup deadline: %v", err)
		return
	}
	if err := conn.DeleteTopics(topic); err != nil {
		t.Errorf("delete temporary Post Embedding topic: %v", err)
	}
}

func writePostEmbeddingIntegrationMessage(t *testing.T, broker, topic string, partition int, value string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := kafka.DialLeader(ctx, "tcp", broker, topic, partition)
	if err != nil {
		t.Fatalf("dial leader topic=%s partition=%d: %v", topic, partition, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteMessages(kafka.Message{Value: []byte(value)}); err != nil {
		t.Fatalf("write topic=%s partition=%d value=%s: %v", topic, partition, value, err)
	}
}

func waitForPostEmbeddingGroupMembers(t *testing.T, cfg config.KafkaConfig, expected int, timeout time.Duration) eventing.KafkaGroupDescription {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	var last eventing.KafkaGroupDescription
	for {
		description, err := eventing.KafkaConsumerGroup(ctx, cfg, cfg.PostEmbeddingGroupID)
		if err == nil {
			last = description
			if description.State == "Stable" && len(description.Members) == expected {
				return description
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Kafka group did not stabilize with %d members; last=%+v err=%v", expected, last, lastErr)
		case <-ticker.C:
		}
	}
}

func waitForPostEmbeddingGenerationNotices(t *testing.T, notices <-chan postEmbeddingGenerationNotice, clients ...string) map[string]postEmbeddingGenerationNotice {
	t.Helper()
	wanted := make(map[string]bool, len(clients))
	for _, clientID := range clients {
		wanted[clientID] = true
	}
	latest := make(map[string]postEmbeddingGenerationNotice, len(clients))
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for len(latest) < len(wanted) {
		select {
		case notice := <-notices:
			if wanted[notice.clientID] {
				latest[notice.clientID] = notice
			}
		case <-timeout.C:
			t.Fatalf("did not observe generation assignments for every member: %+v", latest)
		}
	}
	return latest
}

func settlePostEmbeddingGenerationNotices(notices <-chan postEmbeddingGenerationNotice, latest map[string]postEmbeddingGenerationNotice, clients ...string) {
	wanted := make(map[string]bool, len(clients))
	for _, clientID := range clients {
		wanted[clientID] = true
	}
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case notice := <-notices:
			if wanted[notice.clientID] {
				latest[notice.clientID] = notice
			}
		case <-timer.C:
			return
		}
	}
}

func drainPostEmbeddingGenerationNotices(notices <-chan postEmbeddingGenerationNotice) {
	for {
		select {
		case <-notices:
		default:
			return
		}
	}
}

func receivePostEmbeddingGenerationNotice(notices <-chan postEmbeddingGenerationNotice) (postEmbeddingGenerationNotice, bool) {
	select {
	case notice := <-notices:
		return notice, true
	default:
		return postEmbeddingGenerationNotice{}, false
	}
}

func awaitPostEmbeddingProcessedValues(t *testing.T, processed <-chan postEmbeddingProcessedMessage, values []string, timeout time.Duration) map[string]postEmbeddingProcessedMessage {
	t.Helper()
	wanted := make(map[string]bool, len(values))
	for _, value := range values {
		wanted[value] = true
	}
	seen := make(map[string]postEmbeddingProcessedMessage, len(values))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for len(seen) < len(wanted) {
		select {
		case message := <-processed:
			if wanted[message.value] {
				seen[message.value] = message
			}
		case <-ctx.Done():
			t.Fatalf("messages not processed before timeout: wanted=%v seen=%v", values, seen)
		}
	}
	return seen
}

func waitForPostEmbeddingLagZero(t *testing.T, cfg config.KafkaConfig, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastLag int64 = -1
	var lastErr error
	for {
		lag, err := eventing.KafkaCommittedLag(ctx, cfg, cfg.PostEmbeddingTopic, cfg.PostEmbeddingGroupID)
		if err == nil {
			lastLag = lag
			if lag == 0 {
				return
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Kafka lag did not reach zero; last lag=%d err=%v", lastLag, lastErr)
		case <-ticker.C:
		}
	}
}
