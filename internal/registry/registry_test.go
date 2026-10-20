package registry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shiva2021/purge/internal/ext"
)

// Minimal recorded bodies, trimmed to the fields the parser reads plus a few
// neighbours so the shape stays faithful to the live responses.
const (
	mpVerifiedBody = `{"results":[{"extensions":[{
	  "publisher":{"publisherId":"998b010b-e2af-44a5-a6cd-0b5fd3b9b6f8","publisherName":"ms-python","displayName":"Microsoft","flags":"verified","domain":"https://microsoft.com","isDomainVerified":true},
	  "extensionId":"f1f59ae4-9318-4f3c-a9b5-81b2eaa5f8a5","extensionName":"python","displayName":"Python","flags":"validated, public",
	  "lastUpdated":"2026-10-01T10:00:00.000Z","publishedDate":"2016-01-19T15:03:11.337Z","releaseDate":"2016-01-19T15:03:11.3370000Z",
	  "versions":[{"version":"2026.16.0","flags":"validated","lastUpdated":"2026-10-01T10:00:00.000Z",
	    "properties":[{"key":"Microsoft.VisualStudio.Code.Engine","value":"^1.96.0"},{"key":"Microsoft.VisualStudio.Services.Links.Source","value":"https://github.com/microsoft/vscode-python.git"}]}],
	  "statistics":[{"statisticName":"install","value":1.23456789E8},{"statisticName":"averagerating","value":4.2}],
	  "deploymentType":0}],"pagingToken":null,"resultMetadata":[{"metadataType":"ResultCount","metadataItems":[{"name":"TotalCount","count":1}]}]}]}`

	mpUnverifiedBody = `{"results":[{"extensions":[{
	  "publisher":{"publisherName":"someone","displayName":"Someone","flags":"none","isDomainVerified":false},
	  "extensionName":"thing","publishedDate":"2026-09-20T00:00:00Z",
	  "versions":[{"version":"0.0.1","properties":[{"key":"Microsoft.VisualStudio.Code.Engine","value":"^1.0.0"}]}],
	  "statistics":[{"statisticName":"install","value":12.0}]}]}]}`

	// Publisher flag says verified even though the domain check is false: the
	// gallery has shown both shapes, either should count.
	mpFlagOnlyBody = `{"results":[{"extensions":[{
	  "publisher":{"publisherName":"x","flags":"verified","isDomainVerified":false},
	  "extensionName":"y","publishedDate":"2026-01-01T00:00:00Z",
	  "versions":[{"version":"1.0.0","properties":[{"key":"Microsoft.VisualStudio.Services.Links.GitHub","value":"https://github.com/x/y"}]}],
	  "statistics":[]}]}]}`

	mpEmptyBody = `{"results":[{"extensions":[],"pagingToken":null,"resultMetadata":[{"metadataType":"ResultCount","metadataItems":[{"name":"TotalCount","count":0}]}]}]}`

	ovxVerifiedBody = `{"namespaceUrl":"https://open-vsx.org/api/ms-python","reviewsUrl":"https://open-vsx.org/api/ms-python/python/reviews",
	  "name":"python","namespace":"ms-python","version":"2026.16.0","publishedBy":{"loginName":"open-vsx"},
	  "verified":true,"unrelatedPublisher":false,"namespaceAccess":"restricted","publishedWithTrustedPublishing":true,
	  "averageRating":4.5,"downloadCount":4567890,"reviewCount":12,"versionAlias":["latest"],
	  "timestamp":"2026-10-01T12:00:00.123456Z","preRelease":false,"displayName":"Python","license":"MIT",
	  "repository":"https://github.com/microsoft/vscode-python","deprecated":false,"downloadable":true}`

	ovxUnverifiedBody = `{"name":"thing","namespace":"someone","version":"0.0.1","verified":false,"downloadCount":3,
	  "timestamp":"2026-10-18T00:00:00Z","deprecated":true,"downloadable":true}`

	ovxNotFoundBody = `{"error":"Extension not found: nobody.nothing"}`
)

var fixedNow = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)

func newClient(t *testing.T, mpURL, ovxURL string) *Client {
	t.Helper()
	c := New()
	c.MarketplaceURL = mpURL
	c.OpenVSXURL = ovxURL
	c.Now = func() time.Time { return fixedNow }
	return c
}

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func vscodeExt(publisher, name string) ext.Extension {
	return ext.Extension{Editor: "vscode", ID: strings.ToLower(publisher + "." + name), Publisher: publisher, Name: name}
}

func cursorExt(publisher, name string) ext.Extension {
	return ext.Extension{Editor: "cursor", ID: strings.ToLower(publisher + "." + name), Publisher: publisher, Name: name}
}

func TestMarketplaceRequestShape(t *testing.T) {
	UserAgent = "purge/test"
	var got mpQuery
	var hdr http.Header
	var method string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		hdr = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		_, _ = io.WriteString(w, mpVerifiedBody)
	})
	c := newClient(t, srv.URL, "http://127.0.0.1:0")

	// Sent as stored in the manifest (mixed case); the gallery is case-insensitive.
	m, err := c.Lookup(context.Background(), vscodeExt("MS-Python", "Python"))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("method = %s, want POST", method)
	}
	for k, want := range map[string]string{
		"Accept":       "application/json;api-version=3.0-preview.1",
		"Content-Type": "application/json",
		"User-Agent":   "purge/test",
	} {
		if h := hdr.Get(k); h != want {
			t.Errorf("header %s = %q, want %q", k, h, want)
		}
	}
	if got.Flags != 912 || len(got.Filters) != 1 {
		t.Fatalf("query = %+v, want flags 912 and one filter", got)
	}
	f := got.Filters[0]
	if f.PageNumber != 1 || f.PageSize != 1 {
		t.Errorf("paging = %d/%d, want 1/1", f.PageNumber, f.PageSize)
	}
	wantCrit := []mpCriterion{{7, "MS-Python.Python"}, {8, "Microsoft.VisualStudio.Code"}}
	if len(f.Criteria) != 2 || f.Criteria[0] != wantCrit[0] || f.Criteria[1] != wantCrit[1] {
		t.Errorf("criteria = %+v, want %+v", f.Criteria, wantCrit)
	}

	if !m.Verified || m.Installs != 123456789 || !m.HasRepository || m.Trusted || m.Source != "marketplace" {
		t.Errorf("parsed = %+v", m)
	}
	// 2016-01-19 -> 2026-10-20
	wantAge := int(fixedNow.Sub(time.Date(2016, 1, 19, 15, 3, 11, 0, time.UTC)).Hours() / 24)
	if m.PublisherAge != wantAge {
		t.Errorf("PublisherAge = %d, want %d", m.PublisherAge, wantAge)
	}
}

func TestMarketplaceParsing(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		wantErr       error  // sentinel to match with errors.Is
		wantErrSubstr string // or a substring
		verified      bool
		installs      int64
		repo          bool
		age           int
	}{
		{name: "verified", status: 200, body: mpVerifiedBody, verified: true, installs: 123456789, repo: true, age: 3926},
		{name: "unverified", status: 200, body: mpUnverifiedBody, verified: false, installs: 12, repo: false, age: 30},
		{name: "flag only counts as verified", status: 200, body: mpFlagOnlyBody, verified: true, installs: 0, repo: true, age: 292},
		{name: "empty results", status: 200, body: mpEmptyBody, wantErr: ErrNotFound},
		{name: "no results array", status: 200, body: `{"results":[]}`, wantErr: ErrNotFound},
		{name: "404", status: 404, body: "", wantErr: ErrNotFound},
		{name: "500", status: 500, body: "boom", wantErrSubstr: "HTTP 500"},
		{name: "malformed json", status: 200, body: `{"results":[{"extensions":[{`, wantErrSubstr: "decode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			c := newClient(t, srv.URL, "")
			m, err := c.Lookup(context.Background(), vscodeExt("p", "n"))
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.wantErrSubstr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErrSubstr)
				}
				return
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Verified != tc.verified || m.Installs != tc.installs || m.HasRepository != tc.repo || m.PublisherAge != tc.age {
				t.Errorf("got %+v, want verified=%v installs=%d repo=%v age=%d", m, tc.verified, tc.installs, tc.repo, tc.age)
			}
			if m.Source != "marketplace" || m.Trusted {
				t.Errorf("source/trusted = %q/%v", m.Source, m.Trusted)
			}
		})
	}
}

func TestOpenVSX(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantErr  error
		wantSub  string
		verified bool
		installs int64
		trusted  bool
		repo     bool
		age      int
	}{
		{name: "verified", status: 200, body: ovxVerifiedBody, verified: true, installs: 4567890, trusted: true, repo: true, age: 18},
		{name: "unverified deprecated", status: 200, body: ovxUnverifiedBody, installs: 3, age: 2},
		{name: "404", status: 404, body: ovxNotFoundBody, wantErr: ErrNotFound},
		{name: "500", status: 500, body: "", wantSub: "HTTP 500"},
		{name: "malformed", status: 200, body: `{"verified":`, wantSub: "decode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			UserAgent = "purge/test"
			var path, ua string
			srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
				path, ua = r.URL.Path, r.Header.Get("User-Agent")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			c := newClient(t, "", srv.URL+"/api/")
			m, err := c.Lookup(context.Background(), cursorExt("ms-python", "python"))
			if path != "/api/ms-python/python" {
				t.Errorf("path = %q, want /api/ms-python/python", path)
			}
			if ua != "purge/test" {
				t.Errorf("User-Agent = %q", ua)
			}
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.wantSub != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantSub)
				}
				return
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Verified != tc.verified || m.Installs != tc.installs || m.Trusted != tc.trusted || m.HasRepository != tc.repo || m.PublisherAge != tc.age || m.Source != "openvsx" {
				t.Errorf("got %+v", m)
			}
		})
	}
}

func TestLookupEdgeCases(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, mpVerifiedBody) })
	c := newClient(t, srv.URL, srv.URL)

	t.Run("unknown editor", func(t *testing.T) {
		_, err := c.Lookup(context.Background(), ext.Extension{Editor: "emacs", ID: "a.b", Publisher: "a", Name: "b"})
		if err == nil || !strings.Contains(err.Error(), "emacs") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("falls back to ID when manifest fields are empty", func(t *testing.T) {
		if _, err := c.Lookup(context.Background(), ext.Extension{Editor: "vscode", ID: "a.b"}); err != nil {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no publisher.name", func(t *testing.T) {
		_, err := c.Lookup(context.Background(), ext.Extension{Editor: "vscode", ID: "lonely"})
		if err == nil {
			t.Fatal("want error for malformed id")
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := c.Lookup(ctx, vscodeExt("a", "b"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

func TestLookupAll(t *testing.T) {
	var inFlight, maxInFlightSeen, mpHits, ovxHits atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			cur := maxInFlightSeen.Load()
			if n <= cur || maxInFlightSeen.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond) // widen the window so parallelism is observable

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/mp":
			mpHits.Add(1)
			var q mpQuery
			_ = json.NewDecoder(r.Body).Decode(&q)
			if strings.HasPrefix(q.Filters[0].Criteria[0].Value, "gone.") {
				_, _ = io.WriteString(w, mpEmptyBody)
				return
			}
			_, _ = io.WriteString(w, mpVerifiedBody)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/"):
			ovxHits.Add(1)
			if strings.HasPrefix(r.URL.Path, "/api/broken/") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, ovxVerifiedBody)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	})
	c := newClient(t, srv.URL+"/mp", srv.URL+"/api")

	var exts []ext.Extension
	for i := 0; i < 6; i++ {
		exts = append(exts, vscodeExt("pub", "vs"+string(rune('a'+i))))
	}
	for i := 0; i < 6; i++ {
		exts = append(exts, cursorExt("pub", "cu"+string(rune('a'+i))))
	}
	exts = append(exts,
		vscodeExt("gone", "sideloaded"), // Marketplace: empty -> ErrNotFound
		cursorExt("broken", "server"),   // Open VSX: 500
		ext.Extension{Editor: "vim", ID: "no.registry", Publisher: "no", Name: "registry"},
		vscodeExt("pub", "vsa"), // duplicate ID: looked up once
	)

	got, errs := c.LookupAll(context.Background(), exts)

	if n := maxInFlightSeen.Load(); n > 4 {
		t.Errorf("max in-flight = %d, want <= 4", n)
	} else if n < 2 {
		t.Errorf("max in-flight = %d, expected some parallelism", n)
	}
	if mpHits.Load() != 7 || ovxHits.Load() != 7 {
		t.Errorf("routing: marketplace hits = %d, openvsx hits = %d, want 7/7", mpHits.Load(), ovxHits.Load())
	}
	if len(got) != 12 {
		t.Errorf("results = %d, want 12: %v", len(got), got)
	}
	if len(errs) != 3 {
		t.Errorf("errs = %d, want 3: %v", len(errs), errs)
	}
	if !errors.Is(errs["gone.sideloaded"], ErrNotFound) {
		t.Errorf("gone.sideloaded err = %v, want ErrNotFound", errs["gone.sideloaded"])
	}
	if e := errs["broken.server"]; e == nil || !strings.Contains(e.Error(), "HTTP 500") {
		t.Errorf("broken.server err = %v", e)
	}
	if errs["no.registry"] == nil {
		t.Error("no.registry: want routing error")
	}
	if m := got["pub.vsa"]; m == nil || m.Source != "marketplace" {
		t.Errorf("pub.vsa = %+v, want marketplace result", m)
	}
	if m := got["pub.cua"]; m == nil || m.Source != "openvsx" {
		t.Errorf("pub.cua = %+v, want openvsx result", m)
	}
}

func TestHasFlag(t *testing.T) {
	for _, tc := range []struct {
		flags string
		want  bool
	}{
		{"verified", true}, {"none", false}, {"verified, trusted", true}, {"Verified", true}, {"", false}, {"unverified", false},
	} {
		if got := hasFlag(tc.flags, "verified"); got != tc.want {
			t.Errorf("hasFlag(%q) = %v, want %v", tc.flags, got, tc.want)
		}
	}
}
