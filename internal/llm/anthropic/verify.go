package anthropic

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go/option"
)

// Verify checks that key is accepted by the Claude API with one free request
// (the models list; no tokens, no model needed). It is ListModels without
// the result: a 401/403 is reported as an API key problem and nothing is
// retried. opts are appended after the key, so tests can point at an
// httptest server.
func Verify(ctx context.Context, key string, opts ...option.RequestOption) error {
	_, err := ListModels(ctx, key, opts...)
	return err
}
