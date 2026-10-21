// Package llm calls an OpenAI-compatible chat completions API.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"syscall"
)

// Roles of a chat message.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one turn of a chat.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// maxResponse caps how much of a response body is read.
const maxResponse = 10 << 20

// Client sends chats to one endpoint and model.
type Client struct {
	Endpoint string // base URL, the part before /chat/completions
	Model    string
	APIKey   string         // sent as a Bearer token when set
	Extra    map[string]any // top-level request fields, overriding the defaults
	HTTP     *http.Client
}

// Chat sends messages and returns the reply's content.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	body := map[string]any{
		"model":       c.Model,
		"messages":    messages,
		"temperature": 0.2,
		"stream":      false,
	}
	maps.Copy(body, c.Extra)
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encoding the request: %w", err)
	}

	url := c.Endpoint + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("request to %s failed: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", c.requestError(url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // closing a fully read body has nothing to report
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return "", c.requestError(url, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		hint := ""
		if resp.StatusCode == http.StatusNotFound {
			hint = " (404 usually means a wrong model name or endpoint path)"
		}
		return "", fmt.Errorf("%s returned HTTP %d%s: %s", c.Endpoint, resp.StatusCode, hint, firstRunes(string(data), 500))
	}

	var completion struct {
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &completion) != nil || len(completion.Choices) == 0 || completion.Choices[0].Message.Content == nil {
		return "", fmt.Errorf("%s did not return choices[0].message.content: it's not OpenAI-compatible", c.Endpoint)
	}
	return *completion.Choices[0].Message.Content, nil
}

// requestError explains a failed request, with a friendlier message when the
// server isn't there at all.
func (c *Client) requestError(url string, err error) error {
	var netErr net.Error
	var opErr *net.OpError
	unreachable := errors.As(err, &opErr) && opErr.Op == "dial" ||
		errors.As(err, &netErr) && netErr.Timeout() ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED)
	if unreachable {
		return fmt.Errorf("can't reach %s: is your model server running? (%w)", c.Endpoint, err)
	}
	return fmt.Errorf("request to %s failed: %w", url, err)
}

func firstRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
