package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/shiva2021/rootout/internal/llm"
)

// excludedModelWords mark ids the Responses API adapter cannot use:
// embeddings, speech, images, moderation, the Chat-Completions-only
// search previews and legacy completions ("instruct") models. The models
// endpoint lists every kind.
var excludedModelWords = []string{"embedding", "tts", "whisper", "dall-e", "realtime", "audio", "image", "transcribe", "moderation", "search", "instruct"}

// chatCapable reports whether id looks like a chat model: a gpt-* or o<N>*
// id with none of the excluded words. "chatgpt-*" aliases are skipped too.
func chatCapable(id string) bool {
	gpt := strings.HasPrefix(id, "gpt-")
	o := len(id) >= 2 && id[0] == 'o' && id[1] >= '0' && id[1] <= '9'
	if !gpt && !o {
		return false
	}
	for _, w := range excludedModelWords {
		if strings.Contains(id, w) {
			return false
		}
	}
	return true
}

// ListModels returns the chat models key can use, newest first. It is one
// request (the list is not paged) and is not retried, so a bad key fails
// fast: a 401/403 is reported as an API key problem. Verify is a thin
// wrapper around it. opts are appended after the key for tests.
//
// SDK fields used (openai-go Model): ID, Created, ShutdownDate.
func ListModels(ctx context.Context, key string, opts ...option.RequestOption) ([]llm.ModelInfo, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("openai: %w: paste an API key first", llm.ErrNoKey)
	}
	all := append([]option.RequestOption{option.WithAPIKey(key), option.WithMaxRetries(0)}, opts...)
	client := sdk.NewClient(all...)
	page, err := client.Models.List(ctx)
	if err != nil {
		var apiErr *sdk.Error
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("openai: the API key was rejected (%d): check it at platform.openai.com", apiErr.StatusCode)
		}
		return nil, fmt.Errorf("openai: could not list models: %w", err)
	}
	var out []llm.ModelInfo
	for _, m := range page.Data {
		if !chatCapable(m.ID) {
			continue
		}
		out = append(out, llm.ModelInfo{
			ID:         m.ID,
			Created:    time.Unix(m.Created, 0).UTC(),
			Deprecated: !m.ShutdownDate.IsZero(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}
