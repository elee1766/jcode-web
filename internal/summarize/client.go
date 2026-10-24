// Package summarize produces short summaries with an OpenAI-compatible
// chat completions endpoint. It is meant for a small, fast local model.
package summarize

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	Enabled bool
	BaseURL string
	APIKey  string
	Model   string
	// Passed as reasoning_effort when set. gpt-oss honors low/medium/high.
	ReasoningEffort string
	MaxTokens       int
	Timeout         time.Duration
}

type Client struct {
	cfg  Config
	http *http.Client
	// One request at a time. A local GPU serves them serially anyway.
	sem chan struct{}
}

func New(cfg Config) *Client {
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 400
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}, sem: make(chan struct{}, 1)}
}

func (c *Client) Enabled() bool {
	return c != nil && c.cfg.Enabled && c.cfg.BaseURL != "" && c.cfg.Model != ""
}
func (c *Client) Model() string { return c.cfg.Model }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model           string    `json:"model"`
	Messages        []message `json:"messages"`
	MaxTokens       int       `json:"max_tokens,omitempty"`
	Temperature     float64   `json:"temperature"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	ResponseFormat  any       `json:"response_format,omitempty"`
}

// reportFormat constrains output to the Report schema. Without it gpt-oss on
// LM Studio often fails Harmony parsing with a 400 "peg-native format" error.
var reportFormat = map[string]any{
	"type": "json_schema",
	"json_schema": map[string]any{
		"name":   "report",
		"strict": true,
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":   map[string]any{"type": "string"},
				"summary": map[string]any{"type": "string"},
			},
			"required":             []string{"title", "summary"},
			"additionalProperties": false,
		},
	},
}

type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends one system+user exchange and returns the reply text.
func (c *Client) Complete(ctx context.Context, system, user string) (string, error) {
	return c.complete(ctx, system, user, nil)
}

func (c *Client) complete(ctx context.Context, system, user string, format any) (string, error) {
	if !c.Enabled() {
		return "", errors.New("summarizer not configured")
	}
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	body, _ := json.Marshal(request{
		Model:           c.cfg.Model,
		Messages:        []message{{"system", system}, {"user", user}},
		MaxTokens:       c.cfg.MaxTokens,
		Temperature:     0.2,
		ReasoningEffort: c.cfg.ReasoningEffort,
		ResponseFormat:  format,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("summarizer: %s: %s", resp.Status, truncate(string(raw), 200))
	}
	if r.Error != nil {
		return "", fmt.Errorf("summarizer: %s", r.Error.Message)
	}
	if len(r.Choices) == 0 {
		return "", fmt.Errorf("summarizer: empty response (%s)", resp.Status)
	}
	return strings.TrimSpace(r.Choices[0].Message.Content), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
