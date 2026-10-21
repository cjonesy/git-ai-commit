package config

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cjonesy/git-ai-commit/internal/git"
)

// entries turns "key=value" pairs into config entries, in order.
func entries(pairs ...string) []git.ConfigEntry {
	var es []git.ConfigEntry
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		es = append(es, git.ConfigEntry{Key: k, Value: v})
	}
	return es
}

func TestParseDefaults(t *testing.T) {
	c, err := Parse(entries("ai-commit.model=m"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Endpoint != DefaultEndpoint || c.MaxDiffChars != 12000 || c.Timeout != 300*time.Second || c.CommentPrefix != "#" {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if len(c.Types) != 11 || c.Types[0] != "feat" {
		t.Errorf("Types = %v", c.Types)
	}
}

func TestParseValues(t *testing.T) {
	c, err := Parse(entries(
		"ai-commit.model=first",
		"ai-commit.model=last", // the last value wins
		"ai-commit.endpoint=https://example.com/v1/",
		"ai-commit.types=Feat, fix  docs",
		"ai-commit.exclude=a",
		"ai-commit.exclude=b",
		"ai-commit.maxdiffchars= 500 ",
	))
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "last" || c.Endpoint != "https://example.com/v1" || c.MaxDiffChars != 500 {
		t.Errorf("got %+v", c)
	}
	if !slices.Equal(c.Types, []string{"feat", "fix", "docs"}) || !slices.Equal(c.Exclude, []string{"a", "b"}) {
		t.Errorf("Types = %v, Exclude = %v", c.Types, c.Exclude)
	}
}

func TestParseCommentPrefix(t *testing.T) {
	tests := []struct {
		name  string
		pairs []string
		want  string
	}{
		{"default", nil, "#"},
		{"commentChar", []string{"core.commentchar=;"}, ";"},
		{"commentString", []string{"core.commentstring=//"}, "//"},
		{"synonyms: last wins", []string{"core.commentstring=//", "core.commentchar=;"}, ";"},
		{"auto", []string{"core.commentchar=auto"}, ""},
		{"strip cleanup", []string{"commit.cleanup=strip"}, "#"},
		{"verbatim cleanup", []string{"commit.cleanup=verbatim"}, ""},
		{"scissors cleanup", []string{"commit.cleanup=scissors", "core.commentchar=;"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse(entries(append([]string{"ai-commit.model=m"}, tt.pairs...)...))
			if err != nil {
				t.Fatal(err)
			}
			if c.CommentPrefix != tt.want {
				t.Errorf("CommentPrefix = %q, want %q", c.CommentPrefix, tt.want)
			}
		})
	}
}

func TestParseAPIKeySourceSetLastWins(t *testing.T) {
	tests := []struct {
		name        string
		pairs       []string
		key, keyCmd string
	}{
		{"key then command", []string{"ai-commit.apikey=k", "ai-commit.apikeycommand=echo c"}, "", "echo c"},
		{"command then key", []string{"ai-commit.apikeycommand=echo c", "ai-commit.apikey=k"}, "k", ""},
		{"blank key disables a command", []string{"ai-commit.apikeycommand=echo c", "ai-commit.apikey="}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse(entries(append([]string{"ai-commit.model=m"}, tt.pairs...)...))
			if err != nil {
				t.Fatal(err)
			}
			if c.APIKey != tt.key || c.APIKeyCommand != tt.keyCmd {
				t.Errorf("APIKey = %q, APIKeyCommand = %q; want %q, %q", c.APIKey, c.APIKeyCommand, tt.key, tt.keyCmd)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		pairs   []string
		wantErr string
	}{
		{"no model", nil, "set a model"},
		{"blank model", []string{"ai-commit.model= "}, "set a model"},
		{"empty types", []string{"ai-commit.model=m", "ai-commit.types= , "}, "ai-commit.types is set but empty"},
		{"zero budget", []string{"ai-commit.model=m", "ai-commit.maxdiffchars=0"}, "ai-commit.maxDiffChars must be a positive integer"},
		{"bad timeout", []string{"ai-commit.model=m", "ai-commit.timeout=soon"}, "ai-commit.timeout must be a positive integer"},
		{"extraBody array", []string{"ai-commit.model=m", `ai-commit.extrabody=["x"]`}, "must be a JSON object"},
		{"extraBody null", []string{"ai-commit.model=m", "ai-commit.extrabody=null"}, "must be a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(entries(tt.pairs...))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}
