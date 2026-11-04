// Package openai is the OpenAI adapter for llm.Provider. It uses the official
// SDK's Responses API with a strict JSON schema (D-013).
package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"github.com/shiva2021/rootout/internal/llm"
)

const maxOutputTokens = 1024

type provider struct {
	key, model string
	client     sdk.Client
}

// New returns an OpenAI provider. opts are appended after the key, so tests
// can pass option.WithBaseURL to point at an httptest server.
func New(key, model string, opts ...option.RequestOption) llm.Provider {
	all := append([]option.RequestOption{option.WithAPIKey(key)}, opts...)
	return &provider{key: key, model: model, client: sdk.NewClient(all...)}
}

func (p *provider) Name() string  { return "openai" }
func (p *provider) Model() string { return p.model }

// Complete implements llm.Provider: one Responses call with the schema as
// a strict text format. Authentication failures are mapped here.
func (p *provider) Complete(ctx context.Context, system, user string, schema map[string]any) (string, error) {
	if p.key == "" {
		return "", llm.ErrNoKey
	}
	resp, err := p.client.Responses.New(ctx, responses.ResponseNewParams{
		Model:           p.model,
		Instructions:    sdk.String(system),
		Input:           responses.ResponseNewParamsInputUnion{OfString: sdk.String(user)},
		MaxOutputTokens: sdk.Int(maxOutputTokens),
		Text: responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name:   "assessment",
					Schema: schema,
					Strict: sdk.Bool(true),
				},
			},
		},
	})
	if err != nil {
		var apiErr *sdk.Error
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
			return "", fmt.Errorf("openai: authentication failed, check the API key: %w", err)
		}
		return "", fmt.Errorf("openai: %w", err)
	}
	return resp.OutputText(), nil
}
