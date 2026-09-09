package tasks

import (
	"math"
	"testing"
)

func TestAggregateMaterializerAffinitiesCountsSupportedLanguagesWithoutChangingAuthorAffinity(t *testing.T) {
	contributions := map[uint]float64{
		1: 2,
		2: 3,
		3: 4,
		4: 5,
		5: math.NaN(),
		6: -1,
	}
	rows := []materializerPostMetadata{
		{PostID: 1, AuthorID: 10, Language: "zh"},
		{PostID: 2, AuthorID: 10, Language: "ja"},
		{PostID: 3, AuthorID: 20, Language: "en"},
		{PostID: 4, AuthorID: 20, Language: "und"},
		{PostID: 5, AuthorID: 30, Language: "zh"},
		{PostID: 6, AuthorID: 40, Language: "en"},
	}
	authors, language := aggregateMaterializerAffinities(contributions, rows)
	if len(authors) != 2 || authors[0].AuthorID != 10 || authors[0].RawAffinity != 5 || authors[1].AuthorID != 20 || authors[1].RawAffinity != 9 {
		t.Fatalf("author affinities=%#v", authors)
	}
	if language.LanguageZHWeight != 2 || language.LanguageJAWeight != 3 || language.LanguageENWeight != 4 || language.LanguageEvidence != 9 {
		t.Fatalf("language affinity=%#v", language)
	}
}

func TestAggregateMaterializerAffinitiesViewOnlyDoesNotCreateLanguageEvidence(t *testing.T) {
	authors, language := aggregateMaterializerAffinities(map[uint]float64{1: 1}, []materializerPostMetadata{{PostID: 1, AuthorID: 10, Language: "zh"}})
	if len(authors) != 1 || authors[0].RawAffinity != 1 {
		t.Fatalf("author affinity=%#v", authors)
	}
	// The materializer passes PositiveAffinityContributions here. A view-only
	// profile never enters that map, so an empty contribution map is neutral.
	_, language = aggregateMaterializerAffinities(map[uint]float64{}, []materializerPostMetadata{{PostID: 1, AuthorID: 10, Language: "zh"}})
	if language.LanguageEvidence != 0 || language.LanguageZHWeight != 0 {
		t.Fatalf("view-only language evidence=%#v", language)
	}
}
