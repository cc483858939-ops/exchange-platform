package controllers

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"

	"Go.exchange/config"
)

const (
	topicPostCursorVersion     = 2
	topicShuffleBucketDuration = 12 * time.Hour
)

var (
	errInvalidTopicCursor      = errors.New("invalid cursor")
	generateTopicTraversalSeed = newTopicTraversalSeed
)

type topicPostCursorV2 struct {
	Version    int       `json:"v"`
	TopicHash  string    `json:"topic_hash"`
	AnchorAt   time.Time `json:"anchor_at"`
	Seed       int64     `json:"seed"`
	Bucket     int64     `json:"bucket"`
	ShuffleKey int64     `json:"shuffle_key"`
	PostID     uint      `json:"post_id"`
}

type topicCursorCriteria struct {
	Slug                  string   `json:"slug"`
	SourceKeys            []string `json:"source_keys"`
	BucketDurationSeconds int64    `json:"bucket_seconds"`
}

type topicTraversal struct {
	TopicHash string
	AnchorAt  time.Time
	Seed      int64
	Bucket    int64
	After     *topicPostCursorV2
}

func topicCursorCriteriaHash(topic config.CuratedTopic) (string, error) {
	return topicCursorCriteriaHashForBucketDuration(topic, topicShuffleBucketDuration)
}

func topicCursorCriteriaHashForBucketDuration(topic config.CuratedTopic, bucketDuration time.Duration) (string, error) {
	if bucketDuration <= 0 || bucketDuration%time.Second != 0 {
		return "", errors.New("invalid topic bucket duration")
	}
	sourceKeys := append([]string(nil), topic.SourceKeys...)
	sort.Strings(sourceKeys)
	criteria := topicCursorCriteria{
		Slug:                  topic.Slug,
		SourceKeys:            sourceKeys,
		BucketDurationSeconds: int64(bucketDuration / time.Second),
	}
	encoded, err := json.Marshal(criteria)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func newTopicTraversalSeed() (int64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(raw[:])), nil
}

func resolveTopicTraversal(topic config.CuratedTopic, cursor *topicPostCursorV2) (topicTraversal, error) {
	topicHash, err := topicCursorCriteriaHash(topic)
	if err != nil {
		return topicTraversal{}, err
	}
	if cursor == nil {
		anchorAt := time.Now().UTC()
		seed, err := generateTopicTraversalSeed()
		if err != nil {
			return topicTraversal{}, err
		}
		return topicTraversal{TopicHash: topicHash, AnchorAt: anchorAt, Seed: seed}, nil
	}
	if err := validateTopicPostCursor(*cursor); err != nil || cursor.TopicHash != topicHash {
		return topicTraversal{}, errInvalidTopicCursor
	}
	return topicTraversal{
		TopicHash: topicHash,
		AnchorAt:  cursor.AnchorAt,
		Seed:      cursor.Seed,
		Bucket:    cursor.Bucket,
		After:     cursor,
	}, nil
}

func encodeTopicPostCursor(cursor topicPostCursorV2) (string, error) {
	if err := validateTopicPostCursor(cursor); err != nil {
		return "", err
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeTopicPostCursor(raw, expectedTopicHash string) (topicPostCursorV2, error) {
	if raw == "" {
		return topicPostCursorV2{}, errInvalidTopicCursor
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(payload) == 0 {
		return topicPostCursorV2{}, errInvalidTopicCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var cursor topicPostCursorV2
	if err := decoder.Decode(&cursor); err != nil {
		return topicPostCursorV2{}, errInvalidTopicCursor
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return topicPostCursorV2{}, errInvalidTopicCursor
	}
	if err := validateTopicPostCursor(cursor); err != nil || cursor.TopicHash != expectedTopicHash {
		return topicPostCursorV2{}, errInvalidTopicCursor
	}
	return cursor, nil
}

func validateTopicPostCursor(cursor topicPostCursorV2) error {
	if cursor.Version != topicPostCursorVersion {
		return errInvalidTopicCursor
	}
	hash, err := hex.DecodeString(cursor.TopicHash)
	if err != nil || len(hash) != sha256.Size || hex.EncodeToString(hash) != cursor.TopicHash {
		return errInvalidTopicCursor
	}
	if cursor.AnchorAt.IsZero() || cursor.AnchorAt.Year() < 1 || cursor.AnchorAt.Year() > 9999 ||
		cursor.AnchorAt.After(time.Now().UTC().Add(5*time.Minute)) {
		return errInvalidTopicCursor
	}
	if cursor.Bucket < 0 || cursor.Bucket > maxTopicBucketIndex(cursor.AnchorAt) || cursor.PostID == 0 {
		return errInvalidTopicCursor
	}
	return nil
}

func topicBucketRange(anchorAt time.Time, bucket int64) (time.Time, time.Time, error) {
	if anchorAt.IsZero() || bucket < 0 || bucket > maxTopicBucketIndex(anchorAt) {
		return time.Time{}, time.Time{}, errInvalidTopicCursor
	}
	// Split half-day buckets into whole days plus an optional twelve-hour step
	// so valid older timestamps do not overflow time.Duration multiplication.
	bucketEnd := anchorAt.UTC().AddDate(0, 0, -int(bucket/2))
	if bucket%2 != 0 {
		bucketEnd = bucketEnd.Add(-12 * time.Hour)
	}
	return bucketEnd.Add(-topicShuffleBucketDuration), bucketEnd, nil
}

func topicBucketIndex(anchorAt, sourceCreatedAt time.Time) (int64, error) {
	anchorAt = anchorAt.UTC()
	sourceCreatedAt = sourceCreatedAt.UTC()
	if anchorAt.IsZero() || sourceCreatedAt.After(anchorAt) {
		return 0, errors.New("source timestamp is outside topic traversal anchor")
	}
	deltaSeconds := anchorAt.Unix() - sourceCreatedAt.Unix()
	if anchorAt.Nanosecond() < sourceCreatedAt.Nanosecond() {
		deltaSeconds--
	}
	if deltaSeconds < 0 {
		return 0, errors.New("source timestamp is outside topic traversal anchor")
	}
	return deltaSeconds / int64(topicShuffleBucketDuration/time.Second), nil
}

func maxTopicBucketIndex(anchorAt time.Time) int64 {
	minimum := time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)
	return (anchorAt.UTC().Unix() - minimum.Unix()) / int64(topicShuffleBucketDuration/time.Second)
}
