// Package heur statically scores an extension for malicious behaviour. It
// never executes extension code. Rules are pure functions registered at init
// time from the rule_*.go files; the engine (engine.go) walks files, applies
// them under size caps and turns findings into a 0-100 score.
package heur

import (
	"bytes"
	"sort"
	"strings"
	"sync"

	"github.com/shiva2021/purge/internal/ext"
)

// Severity of one finding.
type Severity int

const (
	Info Severity = iota
	Low
	Medium
	High
	Critical
)

func (s Severity) String() string {
	switch s {
	case Info:
		return "info"
	case Low:
		return "low"
	case Medium:
		return "medium"
	case High:
		return "high"
	case Critical:
		return "critical"
	}
	return "unknown"
}

// MarshalText makes Severity render as its name in JSON.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Weight is the score contribution of one finding of this severity.
func (s Severity) Weight() int {
	return [...]int{0, 5, 15, 30, 50}[s]
}

// Finding is one piece of evidence.
type Finding struct {
	Rule     string   `json:"rule"` // stable snake_case id, e.g. "eval_dynamic"
	Severity Severity `json:"severity"`
	File     string   `json:"file,omitempty"` // relative to the extension dir; "" for manifest-level findings
	Line     int      `json:"line,omitempty"` // 1-based
	Snippet  string   `json:"snippet,omitempty"`
	Detail   string   `json:"detail"`
}

// Report is the result for one extension.
type Report struct {
	Ext       ext.Extension `json:"extension"`
	Score     int           `json:"score"` // 0-100
	Findings  []Finding     `json:"findings"`
	Truncated bool          `json:"truncated"` // caps hit; some files were not scanned
	Scanned   int           `json:"scanned_files"`
}

// Limits bound the walk so a hostile extension cannot exhaust memory or time.
type Limits struct {
	MaxFileBytes  int64 // per file; larger files are skipped (and reported as truncated)
	MaxTotalBytes int64 // per extension
	MaxFiles      int   // per extension
}

// DefaultLimits are used when a zero Limits is passed.
var DefaultLimits = Limits{MaxFileBytes: 2 << 20, MaxTotalBytes: 50 << 20, MaxFiles: 2000}

// FileCtx is what a file rule sees: one source file of the extension.
type FileCtx struct {
	Ext  *ext.Extension
	Rel  string // path relative to Ext.Dir, slash-separated
	Data []byte
	// InNodeModules is true for vendored dependencies, which are noisier and
	// weigh less for obfuscation-style rules.
	InNodeModules bool

	once     sync.Once
	lineOffs []int
	minified *bool
}

// Line returns the 1-based line number of byte offset off.
func (c *FileCtx) Line(off int) int {
	c.once.Do(func() {
		c.lineOffs = []int{0}
		for i, b := range c.Data {
			if b == '\n' {
				c.lineOffs = append(c.lineOffs, i+1)
			}
		}
	})
	return sort.Search(len(c.lineOffs), func(i int) bool { return c.lineOffs[i] > off })
}

// Snippet returns up to max bytes of the line containing off, trimmed.
func (c *FileCtx) Snippet(off, max int) string {
	start := bytes.LastIndexByte(c.Data[:off], '\n') + 1
	end := bytes.IndexByte(c.Data[off:], '\n')
	if end < 0 {
		end = len(c.Data)
	} else {
		end += off
	}
	s := strings.TrimSpace(string(c.Data[start:end]))
	if len(s) > max {
		// keep the match roughly centred
		from := off - start - max/2
		if from < 0 {
			from = 0
		}
		if from+max > len(s) {
			from = len(s) - max
		}
		s = "…" + s[from:from+max] + "…"
	}
	return s
}

// Minified reports whether the file looks machine-generated (very long
// average line length), which lowers the weight of obfuscation findings.
func (c *FileCtx) Minified() bool {
	if c.minified == nil {
		lines := bytes.Count(c.Data, []byte{'\n'}) + 1
		m := len(c.Data)/lines > 400
		c.minified = &m
	}
	return *c.minified
}

// ExtCtx is what an extension-level rule sees: the manifest plus a view of
// the files that were scanned.
type ExtCtx struct {
	Ext   *ext.Extension
	Files []string // relative paths that passed the walker filter
	// Marketplace holds optional --online metadata; nil when offline.
	Marketplace *Marketplace
}

// Marketplace is registry metadata (Marketplace or Open VSX), filled by
// internal/registry when --online is used.
type Marketplace struct {
	Verified      bool   `json:"verified"`
	Installs      int64  `json:"installs"`
	PublisherAge  int    `json:"publisher_age_days"`
	HasRepository bool   `json:"has_repository"`
	Trusted       bool   `json:"trusted_publishing"`
	Source        string `json:"source"` // "marketplace" | "openvsx"
}

// FileRule runs once per scanned file.
type FileRule struct {
	Name  string
	Check func(c *FileCtx) []Finding
}

// ExtRule runs once per extension after all files were seen.
type ExtRule struct {
	Name  string
	Check func(c *ExtCtx) []Finding
}

var (
	regMu     sync.Mutex
	fileRules []FileRule
	extRules  []ExtRule
)

// RegisterFile adds a file rule; called from init() in rule files.
func RegisterFile(r FileRule) {
	regMu.Lock()
	defer regMu.Unlock()
	fileRules = append(fileRules, r)
}

// RegisterExt adds an extension rule; called from init() in rule files.
func RegisterExt(r ExtRule) {
	regMu.Lock()
	defer regMu.Unlock()
	extRules = append(extRules, r)
}

// Rules returns the registered rule names, for `purge scan --list-rules`.
func Rules() (files, exts []string) {
	regMu.Lock()
	defer regMu.Unlock()
	for _, r := range fileRules {
		files = append(files, r.Name)
	}
	for _, r := range extRules {
		exts = append(exts, r.Name)
	}
	sort.Strings(files)
	sort.Strings(exts)
	return files, exts
}

// PerRuleCap limits how much one rule can add to the score, so a noisy rule
// cannot saturate it: twice its severity weight, rounded up by the engine.
const PerRuleCap = 2

// TrustedPublishers get a discount on obfuscation/minification-style findings
// (their bundles are legitimately minified). Lower-case publisher ids.
var TrustedPublishers = map[string]bool{
	"ms-python": true, "ms-vscode": true, "ms-toolsai": true, "ms-dotnettools": true, "ms-azuretools": true,
	"ms-vscode-remote": true, "ms-kubernetes-tools": true, "ms-playwright": true, "ms-vsliveshare": true,
	"github": true, "redhat": true, "golang": true, "rust-lang": true, "dbaeumer": true, "esbenp": true,
	"eamodio": true, "vscodevim": true, "ritwickdey": true, "formulahendry": true, "christian-kohler": true,
	"bradlc": true, "prisma": true, "svelte": true, "vue": true, "astro-build": true, "denoland": true,
	"hashicorp": true, "docker": true, "mongodb": true, "google": true, "googlecloudtools": true,
	"anthropic": true, "openai": true, "continue": true, "sourcegraph": true, "tabnine": true, "codeium": true,
	"streetsidesoftware": true, "usernamehw": true, "pkief": true, "zhuangtongfa": true, "dracula-theme": true,
	"mechatroner": true, "yzhang": true, "davidanson": true, "editorconfig": true, "mikestead": true,
	"wayou": true, "oderwat": true, "gruntfuggly": true, "naumovs": true, "mhutchie": true,
}
