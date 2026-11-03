package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/shiva2021/rootout/internal/llm"
)

// maxModelPages bounds the models listing: five pages of 100 is far more
// than the catalogue holds, and keeps a misbehaving server from looping.
const maxModelPages = 5

// ListModels returns the models key can use, newest first, with retired
// ones dropped. It is one request per page and is not retried, so a bad
// key fails fast: a 401/403 is reported as an API key problem. Verify is a
// thin wrapper around it. opts are appended after the key for tests.
//
// SDK fields used (anthropic-sdk-go ModelInfo): ID, DisplayName, CreatedAt,
// Lifecycle.
func ListModels(ctx context.Context, key string, opts ...option.RequestOption) ([]llm.ModelInfo, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("anthropic: %w: paste an API key first", llm.ErrNoKey)
	}
	all := append([]option.RequestOption{option.WithAPIKey(key), option.WithMaxRetries(0)}, opts...)
	client := sdk.NewClient(all...)
	page, err := client.Models.List(ctx, sdk.ModelListParams{Limit: sdk.Int(100)})
	var out []llm.ModelInfo
	for pages := 0; err == nil && page != nil && pages < maxModelPages; pages++ {
		for _, m := range page.Data {
			if m.Lifecycle == sdk.ModelInfoLifecycleRetired {
				continue
			}
			out = append(out, llm.ModelInfo{
				ID:          m.ID,
				DisplayName: m.DisplayName,
				Created:     m.CreatedAt,
				Deprecated:  m.Lifecycle == sdk.ModelInfoLifecycleDeprecated,
			})
		}
		if !page.HasMore || pages+1 == maxModelPages {
			break
		}
		page, err = page.GetNextPage()
	}
	if err != nil {
		var apiErr *sdk.Error
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("anthropic: the API key was rejected (%d): check it at console.anthropic.com", apiErr.StatusCode)
		}
		return nil, fmt.Errorf("anthropic: could not list models: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}
