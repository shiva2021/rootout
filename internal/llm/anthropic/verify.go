package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/shiva2021/rootout/internal/llm"
)

// Verify checks that key is accepted by the Claude API with one free request
// (the models list; no tokens, no model needed). A 401/403 is reported as an
// API key problem. The request is not retried, so a failure is quick. opts
// are appended after the key, so tests can point at an httptest server.
func Verify(ctx context.Context, key string, opts ...option.RequestOption) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("anthropic: %w: paste an API key first", llm.ErrNoKey)
	}
	all := append([]option.RequestOption{option.WithAPIKey(key), option.WithMaxRetries(0)}, opts...)
	client := sdk.NewClient(all...)
	_, err := client.Models.List(ctx, sdk.ModelListParams{Limit: sdk.Int(1)})
	if err == nil {
		return nil
	}
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
		return fmt.Errorf("anthropic: the API key was rejected (%d): check it at console.anthropic.com", apiErr.StatusCode)
	}
	return fmt.Errorf("anthropic: could not verify the key: %w", err)
}
