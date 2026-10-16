package postmention

import (
	"errors"
	"strings"
	"testing"
)

func TestExtractTokens(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"one", "Thanks @alice", []string{"alice"}},
		{"multiple", "@alice and @bob", []string{"alice", "bob"}},
		{"duplicate", "@alice @alice", []string{"alice"}},
		{"punctuation", "@alice, thanks @bob。", []string{"alice", "bob"}},
		{"email", "mail name@example.com", nil},
		{"url", "see https://example.com/@alice and www.example.com/@bob and mailto:@carol", nil},
		{"double at", "@@alice", nil},
		{"unicode boundary", "中文感谢@alice。再看 @用户_甲", []string{"alice", "用户_甲"}},
		{"dotted handle", "@alice.smith", nil},
		{"exact case spellings are distinct", "@Alice @alice", []string{"Alice", "alice"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ExtractTokens(test.body)
			if err != nil || len(got) != len(test.want) {
				t.Fatalf("ExtractTokens(%q) = %#v, %v; want %#v", test.body, got, err, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("ExtractTokens(%q) = %#v; want %#v", test.body, got, test.want)
				}
			}
		})
	}
}

func TestExtractTokensBoundsDistinctCandidates(t *testing.T) {
	var body strings.Builder
	for i := 0; i < MaxCandidateTokens; i++ {
		body.WriteString(" @person")
		body.WriteString(string(rune('A' + i/26)))
		body.WriteString(string(rune('a' + i%26)))
	}
	if _, err := ExtractTokens(body.String()); err != nil {
		t.Fatalf("at-limit candidates: %v", err)
	}
	body.WriteString(" @overflow")
	if _, err := ExtractTokens(body.String()); !errors.Is(err, ErrTooManyCandidates) {
		t.Fatalf("over-limit candidates error=%v", err)
	}
}

func TestValidPrefix(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		valid bool
	}{{"", "", true}, {"@", "", true}, {"@ali", "ali", true}, {"a_%", "", false}, {strings.Repeat("a", MaxUsernameRunes+1), "", false}} {
		got, ok := ValidPrefix(test.input)
		if got != test.want || ok != test.valid {
			t.Fatalf("ValidPrefix(%q)=(%q,%v), want (%q,%v)", test.input, got, ok, test.want, test.valid)
		}
	}
}

func TestValidUsernameMatchesMentionGrammar(t *testing.T) {
	for _, username := range []string{"alice", "Alice_甲-2", strings.Repeat("界", MaxUsernameRunes)} {
		if !ValidUsername(username) {
			t.Fatalf("ValidUsername(%q)=false", username)
		}
		tokens, err := ExtractTokens("@" + username)
		if err != nil || len(tokens) != 1 || tokens[0] != username {
			t.Fatalf("accepted username %q not fully extracted: tokens=%q err=%v", username, tokens, err)
		}
	}
	for _, username := range []string{"", "alice smith", "alice.name", strings.Repeat("a", MaxUsernameRunes+1)} {
		if ValidUsername(username) {
			t.Fatalf("ValidUsername(%q)=true", username)
		}
	}
}
