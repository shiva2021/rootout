// Package anthropic is the Claude adapter for llm.Provider. It uses the
// official SDK with GA structured outputs (output_config.format), never a
// forced tool choice (D-013).
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/shiva2021/purge/internal/heur"
	"github.com/shiva2021/purge/internal/llm"
)

const maxTokens = 1024

type provider struct {
	key, model string
	client     sdk.Client
}

// New returns a Claude provider. opts are appended after the key, so tests
// can pass option.WithBaseURL to point at an httptest server.
func New(key, model string, opts ...option.RequestOption) llm.Provider {
	all := append([]option.RequestOption{option.WithAPIKey(key)}, opts...)
	return &provider{key: key, model: model, client: sdk.NewClient(all...)}
}

func (p *provider) Name() string  { return "anthropic" }
func (p *provider) Model() string { return p.model }

func (p *provider) Assess(ctx context.Context, r heur.Report) (llm.Assessment, error) {
	if p.key == "" {
		return llm.Assessment{}, llm.ErrNoKey
	}
	system, user := llm.BuildPrompt(r, 0)
	msg, err := p.client.Messages.New(ctx, sdk.MessageNewParams{
		Model:     p.model,
		MaxTokens: maxTokens,
		System:    []sdk.TextBlockParam{{Text: system}},
		Messages:  []sdk.MessageParam{sdk.NewUserMessage(sdk.NewTextBlock(user))},
		OutputConfig: sdk.OutputConfigParam{
			Format: sdk.JSONOutputFormatParam{Schema: llm.Schema()},
		},
	})
	if err != nil {
		var apiErr *sdk.Error
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
			return llm.Assessment{}, fmt.Errorf("anthropic: authentication failed, check the API key: %w", err)
		}
		return llm.Assessment{}, fmt.Errorf("anthropic: %w", err)
	}
	if msg.StopReason == sdk.StopReasonRefusal || msg.StopReason == sdk.StopReasonMaxTokens {
		return llm.Assessment{}, fmt.Errorf("anthropic: %w: stop reason %s", llm.ErrBadResponse, msg.StopReason)
	}
	for _, block := range msg.Content {
		if text, ok := block.AsAny().(sdk.TextBlock); ok {
			a, err := llm.Decode(text.Text)
			if err != nil {
				return llm.Assessment{}, fmt.Errorf("anthropic: %w", err)
			}
			return llm.Normalize(a, p.Name(), p.model), nil
		}
	}
	return llm.Assessment{}, fmt.Errorf("anthropic: %w: no text block", llm.ErrBadResponse)
}
