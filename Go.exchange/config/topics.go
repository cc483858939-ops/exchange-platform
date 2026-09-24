package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const CuratedTopicsVersion = "curated_topics_v1"

var curatedTopicSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type CuratedTopicsConfig struct {
	Version string         `json:"version"`
	Topics  []CuratedTopic `json:"topics"`
}

type CuratedTopic struct {
	Slug        string   `json:"slug"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	SourceKeys  []string `json:"source_keys"`
	Enabled     bool     `json:"enabled"`
}

func (catalog CuratedTopicsConfig) Validate() error {
	if catalog.Version != CuratedTopicsVersion {
		return fmt.Errorf("unsupported curated topics version %q", catalog.Version)
	}
	if len(catalog.Topics) == 0 {
		return errors.New("curated topics list is empty")
	}

	slugs := make(map[string]struct{}, len(catalog.Topics))
	for index, topic := range catalog.Topics {
		if strings.TrimSpace(topic.Slug) == "" {
			return fmt.Errorf("topic %d has an empty slug", index)
		}
		if !curatedTopicSlugPattern.MatchString(topic.Slug) {
			return fmt.Errorf("topic %d has an invalid slug", index)
		}
		if _, exists := slugs[topic.Slug]; exists {
			return fmt.Errorf("topic %d duplicates slug %q", index, topic.Slug)
		}
		slugs[topic.Slug] = struct{}{}
		if strings.TrimSpace(topic.Label) == "" {
			return fmt.Errorf("topic %q has an empty label", topic.Slug)
		}
		if strings.TrimSpace(topic.Description) == "" {
			return fmt.Errorf("topic %q has an empty description", topic.Slug)
		}
		if len(topic.SourceKeys) == 0 {
			return fmt.Errorf("topic %q has an empty source list", topic.Slug)
		}
		sourceKeys := make(map[string]struct{}, len(topic.SourceKeys))
		for _, sourceKey := range topic.SourceKeys {
			if strings.TrimSpace(sourceKey) == "" {
				return fmt.Errorf("topic %q has an empty source key", topic.Slug)
			}
			if _, exists := sourceKeys[sourceKey]; exists {
				return fmt.Errorf("topic %q duplicates source key %q", topic.Slug, sourceKey)
			}
			sourceKeys[sourceKey] = struct{}{}
		}
	}
	return nil
}

func LoadCuratedTopics() (CuratedTopicsConfig, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return CuratedTopicsConfig{}, err
	}

	for directory := workingDirectory; ; directory = filepath.Dir(directory) {
		path := filepath.Join(directory, "config", "sources", "topics.json")
		if _, err := os.Stat(path); err == nil {
			return loadCuratedTopicsFile(path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return CuratedTopicsConfig{}, err
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
	return CuratedTopicsConfig{}, errors.New("curated topics configuration is unavailable")
}

func loadCuratedTopicsFile(path string) (CuratedTopicsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CuratedTopicsConfig{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var catalog CuratedTopicsConfig
	if err := decoder.Decode(&catalog); err != nil {
		return CuratedTopicsConfig{}, fmt.Errorf("decode curated topics: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return CuratedTopicsConfig{}, errors.New("curated topics configuration has trailing JSON")
		}
		return CuratedTopicsConfig{}, fmt.Errorf("decode curated topics: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return CuratedTopicsConfig{}, err
	}
	return catalog, nil
}
