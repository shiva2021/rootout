package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultURL is GitHub's "latest release" endpoint for rootout. GitHub
	// leaves drafts and pre-releases out of it.
	DefaultURL = "https://api.github.com/repos/shiva2021/rootout/releases/latest"

	defaultTimeout = 5 * time.Second
	maxBody        = 1 << 20
)

// Release is one published release: the tag, its page and when it went out.
// The JSON tags are the record file's layout (see ReadRecord).
type Release struct {
	Tag         string    `json:"latest"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at,omitzero"`
}

// Client asks GitHub for the latest release.
type Client struct {
	HTTP      *http.Client
	URL       string // tests point it at an httptest server
	UserAgent string
}

// New returns a client with a 5 s timeout identifying itself as
// rootout/<version>.
func New(version string) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: defaultTimeout},
		URL:       DefaultURL,
		UserAgent: "rootout/" + version,
	}
}

// Latest fetches the latest release. Any non-2xx status, an unreadable body
// or a response without a tag is an error.
func (c *Client) Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return Release{}, fmt.Errorf("update: GET %s: %w", c.URL, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// A transport error from net/http already names the method and URL.
		return Release{}, fmt.Errorf("update: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; nothing to do with a close error
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Release{}, fmt.Errorf("update: GET %s: HTTP %d", c.URL, resp.StatusCode)
	}
	var body struct {
		Tag         string    `json:"tag_name"`
		URL         string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("update: GET %s: decode: %w", c.URL, err)
	}
	if body.Tag == "" {
		return Release{}, fmt.Errorf("update: GET %s: no tag_name in response", c.URL)
	}
	return Release{Tag: body.Tag, URL: body.URL, PublishedAt: body.PublishedAt}, nil
}
