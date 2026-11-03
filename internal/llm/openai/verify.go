package openai

import (
	"context"

	"github.com/openai/openai-go/v3/option"
)

// Verify checks that key is accepted by the OpenAI API with one free request
// (the models list). It is ListModels without the result: a 401/403 is
// reported as an API key problem and nothing is retried. opts are appended
// after the key for tests.
func Verify(ctx context.Context, key string, opts ...option.RequestOption) error {
	_, err := ListModels(ctx, key, opts...)
	return err
}
