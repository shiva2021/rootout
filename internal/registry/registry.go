// Package registry fetches trust metadata for an installed extension from the
// store its editor installs from: the VS Code Marketplace for VS Code and
// Insiders, Open VSX for Cursor, VSCodium and Windsurf. It is only used with
// --online (D-014) and fills heur.Marketplace for the extension-level rules.
package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shiva2021/purge/internal/ext"
	"github.com/shiva2021/purge/internal/heur"
)

const (
	// DefaultMarketplaceURL is the public, unauthenticated Marketplace query endpoint.
	DefaultMarketplaceURL = "https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery"
	// DefaultOpenVSXURL is the Open VSX REST API root.
	DefaultOpenVSXURL = "https://open-vsx.org/api"

	defaultTimeout = 8 * time.Second
	maxBody        = 4 << 20 // a single-extension response is a few KB; cap hostile bodies
	maxInFlight    = 4
)

// UserAgent is sent on every request; cmd/purge sets it to "purge/<version>".
var UserAgent = "purge/dev"

// ErrNotFound is returned when the registry has no such extension. That usually
// means it was sideloaded from a .vsix or has since been removed from the store;
// callers may treat it as a signal.
var ErrNotFound = errors.New("registry: extension not found")

// Client talks to both registries. The zero value is not usable; call New.
type Client struct {
	HTTP           *http.Client // default: 8 s timeout
	MarketplaceURL string       // default DefaultMarketplaceURL
	OpenVSXURL     string       // default DefaultOpenVSXURL
	Now            func() time.Time
}

// New returns a Client with the production defaults.
func New() *Client {
	return &Client{
		HTTP:           &http.Client{Timeout: defaultTimeout},
		MarketplaceURL: DefaultMarketplaceURL,
		OpenVSXURL:     DefaultOpenVSXURL,
		Now:            time.Now,
	}
}

// Lookup fetches trust metadata for e, routing by editor:
//
//	vscode, vscode-insiders -> Marketplace; cursor, vscodium, windsurf -> Open VSX.
//
// Returns ErrNotFound when the registry has no such extension.
func (c *Client) Lookup(ctx context.Context, e ext.Extension) (*heur.Marketplace, error) {
	publisher, name, err := split(e)
	if err != nil {
		return nil, err
	}
	switch e.Editor {
	case "vscode", "vscode-insiders":
		return c.marketplace(ctx, publisher, name)
	case "cursor", "vscodium", "windsurf":
		return c.openVSX(ctx, publisher, name)
	default:
		return nil, fmt.Errorf("registry: no registry for editor %q", e.Editor)
	}
}

// LookupAll runs Lookup for each extension with at most 4 in flight, returning a
// map keyed by Extension.ID. Failures land in errs under the same key and do not
// stop the others. An ID installed in several editors is looked up once, in the
// registry of the editor listed first.
func (c *Client) LookupAll(ctx context.Context, exts []ext.Extension) (map[string]*heur.Marketplace, map[string]error) {
	results := make(map[string]*heur.Marketplace, len(exts))
	errs := make(map[string]error)
	seen := make(map[string]bool, len(exts))

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, maxInFlight)
	)
	for _, e := range exts {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		wg.Add(1)
		go func(e ext.Extension) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m, err := c.Lookup(ctx, e)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[e.ID] = err
				return
			}
			results[e.ID] = m
		}(e)
	}
	wg.Wait()
	return results, errs
}

// split returns the publisher and name as stored in the manifest (original
// case; the Marketplace is case-insensitive, Open VSX namespaces are too),
// falling back to Extension.ID when the manifest fields are empty.
func split(e ext.Extension) (publisher, name string, err error) {
	publisher, name = e.Publisher, e.Name
	if publisher == "" || name == "" {
		publisher, name, _ = strings.Cut(e.ID, ".")
	}
	if publisher == "" || name == "" {
		return "", "", fmt.Errorf("registry: extension %q has no publisher.name", e.ID)
	}
	return publisher, name, nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: defaultTimeout}
}

// do sends req and returns the body for 2xx, ErrNotFound for 404 and an error
// naming the status otherwise. Bodies are capped at maxBody.
func (c *Client) do(req *http.Request, what string) ([]byte, error) {
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry: %s: %w", what, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; nothing to do with a close error
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("registry: %s: read body: %w", what, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("registry: %s: HTTP %d", what, resp.StatusCode)
	}
	return body, nil
}

// ageDays returns whole days from t to now, never negative; 0 for a zero t.
func (c *Client) ageDays(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	d := int(c.now().Sub(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// parseTime accepts RFC 3339 with any number of fractional digits, which covers
// both registries ("2016-01-19T15:03:11.337Z", "...11.3370000Z"). Unparseable
// or empty input yields the zero time rather than an error: a missing date
// only costs the age signal.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// --- VS Code Marketplace -----------------------------------------------------

const (
	mpFilterExtensionName = 7
	mpFilterTarget        = 8
	mpTarget              = "Microsoft.VisualStudio.Code"
	// mpFlags asks for versions, files, properties, statistics and the
	// publisher's verification fields in one query (same value the VS Code
	// client sends).
	mpFlags     = 912
	mpAccept    = "application/json;api-version=3.0-preview.1"
	mpLinkKey   = "Microsoft.VisualStudio.Services.Links.Source"
	mpGitHubKey = "Microsoft.VisualStudio.Services.Links.GitHub"
)

type mpQuery struct {
	Filters []mpFilter `json:"filters"`
	Flags   int        `json:"flags"`
}

type mpFilter struct {
	Criteria   []mpCriterion `json:"criteria"`
	PageNumber int           `json:"pageNumber"`
	PageSize   int           `json:"pageSize"`
}

type mpCriterion struct {
	FilterType int    `json:"filterType"`
	Value      string `json:"value"`
}

type mpResponse struct {
	Results []struct {
		Extensions []mpExtension `json:"extensions"`
	} `json:"results"`
}

type mpExtension struct {
	Publisher struct {
		PublisherName    string `json:"publisherName"`
		Flags            string `json:"flags"` // comma-separated, e.g. "verified"
		IsDomainVerified bool   `json:"isDomainVerified"`
	} `json:"publisher"`
	PublishedDate string `json:"publishedDate"`
	ReleaseDate   string `json:"releaseDate"`
	Versions      []struct {
		Properties []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"properties"`
	} `json:"versions"`
	Statistics []struct {
		StatisticName string  `json:"statisticName"`
		Value         float64 `json:"value"` // the gallery serialises counts as floats
	} `json:"statistics"`
}

func (c *Client) marketplace(ctx context.Context, publisher, name string) (*heur.Marketplace, error) {
	id := publisher + "." + name
	q := mpQuery{
		Filters: []mpFilter{{
			Criteria: []mpCriterion{
				{FilterType: mpFilterExtensionName, Value: id},
				{FilterType: mpFilterTarget, Value: mpTarget},
			},
			PageNumber: 1,
			PageSize:   1,
		}},
		Flags: mpFlags,
	}
	body, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("registry: marketplace %s: encode query: %w", id, err)
	}
	endpoint := c.MarketplaceURL
	if endpoint == "" {
		endpoint = DefaultMarketplaceURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("registry: marketplace %s: %w", id, err)
	}
	req.Header.Set("Accept", mpAccept)
	req.Header.Set("Content-Type", "application/json")

	raw, err := c.do(req, "marketplace "+id)
	if err != nil {
		return nil, err
	}
	var resp mpResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("registry: marketplace %s: decode: %w", id, err)
	}
	if len(resp.Results) == 0 || len(resp.Results[0].Extensions) == 0 {
		return nil, ErrNotFound
	}
	return mpToMarketplace(c, resp.Results[0].Extensions[0]), nil
}

func mpToMarketplace(c *Client, x mpExtension) *heur.Marketplace {
	m := &heur.Marketplace{Source: "marketplace"}
	m.Verified = x.Publisher.IsDomainVerified || hasFlag(x.Publisher.Flags, "verified")

	for _, s := range x.Statistics {
		if s.StatisticName == "install" {
			m.Installs = int64(s.Value)
			break
		}
	}

	// The query response carries no publisher creation date, so publisher age
	// is approximated by this extension's age: the older of its first
	// publication and release dates. A publisher with several extensions may
	// therefore look younger than they are, never older.
	oldest := parseTime(x.PublishedDate)
	if r := parseTime(x.ReleaseDate); !r.IsZero() && (oldest.IsZero() || r.Before(oldest)) {
		oldest = r
	}
	m.PublisherAge = c.ageDays(oldest)

	if len(x.Versions) > 0 {
		for _, p := range x.Versions[0].Properties {
			if (p.Key == mpLinkKey || p.Key == mpGitHubKey) && strings.TrimSpace(p.Value) != "" {
				m.HasRepository = true
				break
			}
		}
	}
	return m
}

// hasFlag reports whether a comma-separated gallery flags string contains flag.
func hasFlag(flags, flag string) bool {
	for _, f := range strings.Split(flags, ",") {
		if strings.EqualFold(strings.TrimSpace(f), flag) {
			return true
		}
	}
	return false
}

// --- Open VSX ----------------------------------------------------------------

type ovxExtension struct {
	Verified                       bool   `json:"verified"`
	DownloadCount                  int64  `json:"downloadCount"`
	PublishedWithTrustedPublishing bool   `json:"publishedWithTrustedPublishing"`
	Timestamp                      string `json:"timestamp"`  // RFC 3339, latest version's publish time
	Repository                     string `json:"repository"` // "" when the manifest has none
	// Deprecated marks extensions Open VSX flags as superseded; it is parsed
	// so the field is visible but heur.Marketplace has no slot for it yet.
	Deprecated bool `json:"deprecated"`
}

func (c *Client) openVSX(ctx context.Context, publisher, name string) (*heur.Marketplace, error) {
	id := publisher + "." + name
	root := c.OpenVSXURL
	if root == "" {
		root = DefaultOpenVSXURL
	}
	endpoint := strings.TrimSuffix(root, "/") + "/" + url.PathEscape(publisher) + "/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("registry: openvsx %s: %w", id, err)
	}
	req.Header.Set("Accept", "application/json")

	raw, err := c.do(req, "openvsx "+id)
	if err != nil {
		return nil, err
	}
	var x ovxExtension
	if err := json.Unmarshal(raw, &x); err != nil {
		return nil, fmt.Errorf("registry: openvsx %s: decode: %w", id, err)
	}
	// Open VSX exposes no first-publication or namespace creation date in this
	// response, so age is approximated by the latest version's timestamp.
	return &heur.Marketplace{
		Verified:      x.Verified,
		Installs:      x.DownloadCount,
		PublisherAge:  c.ageDays(parseTime(x.Timestamp)),
		HasRepository: strings.TrimSpace(x.Repository) != "",
		Trusted:       x.PublishedWithTrustedPublishing,
		Source:        "openvsx",
	}, nil
}
