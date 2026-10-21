// Package config turns git config entries into git-ai-commit's settings.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cjonesy/git-ai-commit/internal/git"
	"github.com/cjonesy/git-ai-commit/internal/message"
)

// DefaultEndpoint is Ollama's OpenAI-compatible API.
const DefaultEndpoint = "http://localhost:11434/v1"

// Config holds the ai-commit.* settings and the git settings they depend on.
type Config struct {
	Endpoint      string
	Model         string
	APIKey        string
	APIKeyCommand string // run by ResolveAPIKey to get APIKey
	Types         []string
	MaxDiffChars  int
	Timeout       time.Duration
	Exclude       []string       // git pathspec patterns whose content isn't sent
	ExtraBody     map[string]any // merged into every request body
	// CommentPrefix is the line prefix `git commit -e` strips as a comment,
	// or "" when it strips nothing.
	CommentPrefix string
}

// Parse reads the settings from entries, which are in precedence order.
func Parse(entries []git.ConfigEntry) (*Config, error) {
	v := make(values)
	// apiKey and apiKeyCommand are two ways to set one thing, so whichever
	// is set last wins, letting a repository override a global setting
	var keySource string
	for _, e := range entries {
		key := strings.ToLower(e.Key)
		switch key {
		case "core.commentstring":
			key = "core.commentchar" // synonyms: whichever is set last wins
		case "ai-commit.apikey", "ai-commit.apikeycommand":
			keySource = key
		}
		v[key] = append(v[key], e.Value)
	}

	c := &Config{
		Endpoint:      strings.TrimRight(v.get("ai-commit.endpoint", DefaultEndpoint), "/"),
		Model:         strings.TrimSpace(v.get("ai-commit.model", "")),
		Exclude:       v["ai-commit.exclude"],
		CommentPrefix: commentPrefix(v),
	}
	switch keySource {
	case "ai-commit.apikey":
		c.APIKey = v.get(keySource, "")
	case "ai-commit.apikeycommand":
		c.APIKeyCommand = strings.TrimSpace(v.get(keySource, ""))
	}
	if c.Model == "" {
		return nil, errors.New("set a model: git config --global ai-commit.model <name>")
	}

	var err error
	if c.Types, err = types(v); err != nil {
		return nil, err
	}
	if c.MaxDiffChars, err = positive(v, "ai-commit.maxDiffChars", 12000); err != nil {
		return nil, err
	}
	seconds, err := positive(v, "ai-commit.timeout", 300)
	if err != nil {
		return nil, err
	}
	c.Timeout = time.Duration(seconds) * time.Second
	if c.ExtraBody, err = extraBody(v); err != nil {
		return nil, err
	}
	return c, nil
}

// ResolveAPIKey runs APIKeyCommand, if set, and uses what it prints as the
// API key: a short-lived token or a secret from a password manager. The
// command keeps the terminal's stdin and stderr so it can prompt.
func (c *Config) ResolveAPIKey(ctx context.Context) error {
	if c.APIKeyCommand == "" {
		return nil
	}
	// sh, like git's own command settings; Windows needs Git's sh on PATH
	cmd := exec.CommandContext(ctx, "sh", "-c", c.APIKeyCommand)
	cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ai-commit.apiKeyCommand failed: %w", err)
	}
	if c.APIKey = strings.TrimSpace(string(out)); c.APIKey == "" {
		return errors.New("ai-commit.apiKeyCommand printed nothing")
	}
	return nil
}

// values maps lowercased keys to their values in precedence order.
type values map[string][]string

// get returns the winning value of key, or def when it isn't set.
func (v values) get(key, def string) string {
	if vals := v[key]; len(vals) > 0 {
		return vals[len(vals)-1]
	}
	return def
}

func types(v values) ([]string, error) {
	list, ok := v["ai-commit.types"]
	if !ok {
		names := make([]string, len(message.DefaultTypes))
		for i, t := range message.DefaultTypes {
			names[i] = t.Name
		}
		return names, nil
	}
	names := strings.FieldsFunc(strings.ToLower(list[len(list)-1]), func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	if len(names) == 0 {
		return nil, errors.New("ai-commit.types is set but empty")
	}
	return names, nil
}

// positive reads an integer setting that must be above zero.
func positive(v values, key string, def int) (int, error) {
	s, ok := v[strings.ToLower(key)]
	if !ok {
		return def, nil
	}
	raw := s[len(s)-1]
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, raw)
	}
	return n, nil
}

func extraBody(v values) (map[string]any, error) {
	raw := v.get("ai-commit.extrabody", "")
	if raw == "" {
		return nil, nil
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil || body == nil {
		return nil, fmt.Errorf("ai-commit.extraBody must be a JSON object, got %q", raw)
	}
	return body, nil
}

// commentPrefix returns what `git commit -e` strips as a comment. Only the
// default and `strip` cleanup modes strip comments, and with core.commentChar
// set to `auto` git picks a prefix that no line of the message starts with.
func commentPrefix(v values) string {
	switch v.get("commit.cleanup", "default") {
	case "default", "strip":
	default:
		return ""
	}
	switch c := v.get("core.commentchar", ""); c {
	case "":
		return "#"
	case "auto":
		return ""
	default:
		return c
	}
}
