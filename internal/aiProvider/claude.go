package aiProvider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ClaudeProvider calls Anthropic's Messages API with SSE streaming.
// Structured output uses a single "emit" tool whose input_schema is the caller's Schema.
type ClaudeProvider struct {
	endpoint string       // endpoint is the base URL, e.g. https://api.anthropic.com
	apiKey   string       // apiKey is the Anthropic API key
	httpCli  *http.Client // httpCli is the underlying HTTP client
}

// NewClaudeProvider constructs a ClaudeProvider pointed at endpoint.
// Pass https://api.anthropic.com in production.
func NewClaudeProvider(endpoint, apiKey string) *ClaudeProvider {
	return &ClaudeProvider{
		endpoint: endpoint,
		apiKey:   apiKey,
		httpCli:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Stream submits a Messages request and returns a channel of partial tool-input JSON text.
func (p *ClaudeProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	body, err := buildMessagesBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := p.newHTTPRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpCli.Do(httpReq)
	if err != nil {
		return nil, wrapProviderErr(err)
	}
	if resp.StatusCode != http.StatusOK {
		drainAndCloseWithError(resp)
		return nil, providerStatusErr(resp.StatusCode)
	}
	out := make(chan Chunk, 32)
	go pumpSSE(ctx, resp, out)
	return out, nil
}

// newHTTPRequest constructs the POST with required Anthropic headers.
func (p *ClaudeProvider) newHTTPRequest(ctx context.Context, body []byte) (*http.Request, error) {
	url := strings.TrimRight(p.endpoint, "/") + "/v1/messages"
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request:\n%w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", p.apiKey)
	r.Header.Set("anthropic-version", "2023-06-01")
	return r, nil
}

// buildMessagesBody assembles the JSON body for Anthropic's /v1/messages endpoint.
func buildMessagesBody(req Request) ([]byte, error) {
	content := buildUserContent(req)
	tools := buildTools(req.Schema)
	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": orDefaultInt(req.MaxTokens, 4096),
		"stream":     true,
		"messages":   []map[string]any{{"role": "user", "content": content}},
	}
	if tools != nil {
		payload["tools"] = tools
		payload["tool_choice"] = map[string]any{"type": "tool", "name": "emit"}
	}
	return json.Marshal(payload)
}

// buildUserContent assembles the "content" array: optional images, then the prompt text.
func buildUserContent(req Request) []map[string]any {
	parts := make([]map[string]any, 0, len(req.Images)+1)
	for _, img := range req.Images {
		parts = append(parts, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": img.MediaType,
				"data":       base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	parts = append(parts, map[string]any{"type": "text", "text": req.Prompt})
	return parts
}

// buildTools returns a single "emit" tool whose input_schema is the caller's schema.
func buildTools(schema []byte) []map[string]any {
	if len(schema) == 0 {
		return nil
	}
	var raw any
	if json.Unmarshal(schema, &raw) != nil {
		raw = map[string]any{"type": "object"}
	}
	return []map[string]any{{
		"name":         "emit",
		"description":  "Emit the structured output required by the caller.",
		"input_schema": raw,
	}}
}

// orDefaultInt returns v unless it's zero, in which case it returns fallback.
func orDefaultInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}
