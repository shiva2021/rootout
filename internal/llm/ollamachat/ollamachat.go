// Package ollamachat is the Ollama adapter for llm.Provider. It calls the
// local server's POST /api/chat with a JSON schema in "format", using only
// net/http (D-013). Model discovery lives in internal/llm/ollama.
package ollamachat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
)

// DefaultURL is Ollama's standard local address.
const DefaultURL = "http://127.0.0.1:11434"

// maxBody caps how much of a reply is read.
const maxBody = 4 << 20

// ErrNoModel means no Ollama model has been chosen yet.
var ErrNoModel = errors.New("no Ollama model selected; run rootout config models")

type provider struct {
	baseURL, model string
	hc             *http.Client
}

// New returns an Ollama provider. baseURL "" means DefaultURL; hc nil means
// http.DefaultClient (the caller's context bounds the request).
func New(baseURL, model string, hc *http.Client) llm.Provider {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &provider{baseURL: strings.TrimRight(baseURL, "/"), model: model, hc: hc}
}

func (p *provider) Name() string  { return "ollama" }
func (p *provider) Model() string { return p.model }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []message      `json:"messages"`
	Stream   bool           `json:"stream"`
	Think    bool           `json:"think"`            // false: thinking models (qwen3) answer directly
	Format   map[string]any `json:"format,omitempty"` // omitted on the no-schema fallback
	Options  map[string]any `json:"options"`
}

type chatResponse struct {
	Message message `json:"message"`
	Done    bool    `json:"done"`
}

func (p *provider) Assess(ctx context.Context, r heur.Report) (llm.Assessment, error) {
	if p.model == "" {
		return llm.Assessment{}, ErrNoModel
	}
	system, user := llm.BuildPrompt(r, 0)
	msgs := []message{{Role: "system", Content: system}, {Role: "user", Content: user}}
	content, err := p.chat(ctx, msgs, true)
	if errors.Is(err, errNoStructured) {
		// Some models on some Ollama versions cannot do grammar-constrained
		// output ("structured output is unavailable"). The prompt already
		// demands the JSON shape, so ask again without a format and let
		// Decode pull the object out of the reply.
		content, err = p.chat(ctx, msgs, false)
	}
	if err != nil {
		return llm.Assessment{}, err
	}
	a, err := llm.Decode(content)
	if err != nil {
		return llm.Assessment{}, fmt.Errorf("ollama: %w", err)
	}
	return llm.Normalize(a, p.Name(), p.model), nil
}

// errNoStructured marks Ollama's refusal to apply a format for this model.
var errNoStructured = errors.New("ollama: structured output is unavailable for this model")

// chat sends one request; withFormat attaches the JSON schema.
func (p *provider) chat(ctx context.Context, msgs []message, withFormat bool) (string, error) {
	cr := chatRequest{
		Model:    p.model,
		Messages: msgs,
		Stream:   false,
		Think:    false,
		Options:  map[string]any{"temperature": 0},
	}
	if withFormat {
		cr.Format = llm.Schema()
	}
	body, err := json.Marshal(cr)
	if err != nil {
		return "", fmt.Errorf("ollama: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama: %s: %w", p.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("ollama: read reply: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", fmt.Errorf("ollama: authentication failed (%s): %s", resp.Status, apiError(data))
	case resp.StatusCode == http.StatusNotImplemented && strings.Contains(strings.ToLower(apiError(data)), "structured output"):
		return "", errNoStructured
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("ollama: %s: %s", resp.Status, apiError(data))
	}
	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("ollama: %w: %w", llm.ErrBadResponse, err)
	}
	return out.Message.Content, nil
}

// apiError extracts Ollama's {"error": "..."} message, falling back to the
// raw body.
func apiError(data []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error != "" {
		return e.Error
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
