package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadCuratedTopicsV1(t *testing.T) {
	catalog, err := loadCuratedTopicsFile(filepath.Join("sources", "topics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != CuratedTopicsVersion || len(catalog.Topics) != 6 {
		t.Fatalf("catalog version=%q topics=%d", catalog.Version, len(catalog.Topics))
	}
	wantSlugs := []string{"japan", "ai", "space", "business", "culture", "creators"}
	gotSlugs := make([]string, len(catalog.Topics))
	for index, topic := range catalog.Topics {
		gotSlugs[index] = topic.Slug
		if !topic.Enabled {
			t.Errorf("topic %q is disabled", topic.Slug)
		}
	}
	if !reflect.DeepEqual(gotSlugs, wantSlugs) {
		t.Fatalf("topic order=%v, want %v", gotSlugs, wantSlugs)
	}
}

func TestCuratedTopicsValidationRejectsInvalidDefinitions(t *testing.T) {
	validTopic := CuratedTopic{Slug: "topic-one", Label: "Topic", Description: "Description", SourceKeys: []string{"source"}, Enabled: true}
	base := CuratedTopicsConfig{Version: CuratedTopicsVersion, Topics: []CuratedTopic{validTopic}}
	tests := []struct {
		name   string
		mutate func(*CuratedTopicsConfig)
	}{
		{name: "unsupported version", mutate: func(c *CuratedTopicsConfig) { c.Version = "future" }},
		{name: "empty topic list", mutate: func(c *CuratedTopicsConfig) { c.Topics = nil }},
		{name: "empty slug", mutate: func(c *CuratedTopicsConfig) { c.Topics[0].Slug = " " }},
		{name: "invalid slug", mutate: func(c *CuratedTopicsConfig) { c.Topics[0].Slug = "Topic_One" }},
		{name: "duplicate slug", mutate: func(c *CuratedTopicsConfig) { c.Topics = append(c.Topics, validTopic) }},
		{name: "empty label", mutate: func(c *CuratedTopicsConfig) { c.Topics[0].Label = " " }},
		{name: "empty description", mutate: func(c *CuratedTopicsConfig) { c.Topics[0].Description = " " }},
		{name: "empty source list", mutate: func(c *CuratedTopicsConfig) { c.Topics[0].SourceKeys = nil }},
		{name: "duplicate source key", mutate: func(c *CuratedTopicsConfig) { c.Topics[0].SourceKeys = []string{"source", "source"} }},
		{name: "empty source key", mutate: func(c *CuratedTopicsConfig) { c.Topics[0].SourceKeys = []string{" "} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := CuratedTopicsConfig{Version: base.Version, Topics: append([]CuratedTopic(nil), base.Topics...)}
			catalog.Topics[0].SourceKeys = append([]string(nil), base.Topics[0].SourceKeys...)
			test.mutate(&catalog)
			if err := catalog.Validate(); err == nil {
				t.Fatal("Validate() succeeded for invalid topic config")
			}
		})
	}
}

func TestCuratedTopicSourcesExistInSourceRegistry(t *testing.T) {
	catalog, err := loadCuratedTopicsFile(filepath.Join("sources", "topics.json"))
	if err != nil {
		t.Fatal(err)
	}
	registryBytes, err := os.ReadFile(filepath.Join("sources", "x_sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Accounts []struct {
			Key string `json:"key"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(registryBytes, &registry); err != nil {
		t.Fatal(err)
	}
	known := make(map[string]struct{}, len(registry.Accounts))
	for _, account := range registry.Accounts {
		known[account.Key] = struct{}{}
	}
	excluded := map[string]struct{}{"visualsofearth1": {}, "NintendoAmerica": {}, "CuddlyCutePets": {}}
	for _, topic := range catalog.Topics {
		for _, key := range topic.SourceKeys {
			if _, ok := known[key]; !ok {
				t.Errorf("topic %q references unknown source key %q", topic.Slug, key)
			}
			if _, ok := excluded[key]; ok {
				t.Errorf("excluded source key %q appears in topic %q", key, topic.Slug)
			}
		}
	}
}
