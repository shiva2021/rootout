package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/shiva2021/rootout/internal/llm"
)

// Verify checks that key is accepted by the OpenAI API with one free request
// (the models list). A 401/403 is reported as an API key problem. The
// request is not retried. opts are appended after the key for tests.
func Verify(ctx context.Context, key string, opts ...option.RequestOption) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("openai: %w: paste an API key first", llm.ErrNoKey)
	}
	all := append([]option.RequestOption{option.WithAPIKey(key), option.WithMaxRetries(0)}, opts...)
	client := sdk.NewClient(all...)
	_, err := client.Models.List(ctx)
	if err == nil {
		return nil
	}
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
		return fmt.Errorf("openai: the API key was rejected (%d): check it at platform.openai.com", apiErr.StatusCode)
	}
	return fmt.Errorf("openai: could not verify the key: %w", err)
}
