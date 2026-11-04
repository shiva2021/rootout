package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/run"
)

// Per-probe timeouts. spctl may consult Apple's notary service, so it gets
// the longest; variables rather than constants so tests can shorten them.
var (
	codesignTimeout = 10 * time.Second
	spctlTimeout    = 20 * time.Second
	mdlsTimeout     = 5 * time.Second
)

// Facts is everything rootout can learn about an installed app locally:
// what the bundle says about itself, who signed it, whether Gatekeeper
// accepts it, where it was downloaded from and what it registers with the
// system. It is the evidence an AI assessment is built from, so every string
// in it is untrusted data from the bundle, Spotlight or Homebrew.
type Facts struct {
	App         App        `json:"app"`
	Signature   Signature  `json:"signature"`
	Gatekeeper  Gatekeeper `json:"gatekeeper"`
	Origin      Origin     `json:"origin"`
	AppStore    bool       `json:"app_store"`              // Contents/_MASReceipt/receipt exists
	Plist       PlistFacts `json:"plist"`                  // Info.plist keys beyond the ones App carries
	LaunchItems []string   `json:"launch_items,omitempty"` // LaunchAgents/LaunchDaemons plists whose name starts with the bundle id
	Unavailable []string   `json:"unavailable,omitempty"`  // probes that failed or timed out: codesign, spctl, mdls, plist
}

// Inspect gathers Facts for a. Every probe is fail-soft: a command that
// cannot run, returns nothing or exceeds its timeout is listed in
// Unavailable and the rest of the facts stand. The three commands run
// concurrently under their own timeouts. Probes see the resolved bundle
// path (a cask symlink carries no Spotlight metadata); App.Path is kept as
// given for display. App.TeamID is filled from the signature.
func Inspect(ctx context.Context, env fsx.Env, a App, r run.Runner) Facts {
	f := Facts{App: a}
	target := a.Path
	if real, err := filepath.EvalSymlinks(a.Path); err == nil {
		target = real
	}

	var (
		mu          sync.Mutex
		unavailable []string
		wg          sync.WaitGroup
	)
	// probe runs one command and hands its output to parse, which reports
	// whether the output was an answer. A command cut short by its timeout
	// or by ctx is never an answer, whatever it printed before the kill:
	// a partial codesign report would read as a weak signature.
	probe := func(name string, timeout time.Duration, parse func([]byte) bool, args ...string) {
		wg.Go(func() {
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			// run.Default returns combined output, which matters here:
			// codesign prints its report on stderr. The exit status is
			// not consulted: unsigned (1) and rejected (3) are answers.
			out, _ := r(pctx, name, append(args, target)...)
			if pctx.Err() != nil || len(bytes.TrimSpace(out)) == 0 || !parse(out) {
				mu.Lock()
				unavailable = append(unavailable, name)
				mu.Unlock()
			}
		})
	}
	probe("codesign", codesignTimeout, func(out []byte) (ok bool) { f.Signature, ok = parseCodesign(out); return ok }, "-dv", "--verbose=4")
	probe("spctl", spctlTimeout, func(out []byte) (ok bool) { f.Gatekeeper, ok = parseSpctl(out); return ok }, "--assess", "--type", "execute", "-vv")
	probe("mdls", mdlsTimeout, func(out []byte) (ok bool) { f.Origin, ok = parseMdls(out); return ok },
		"-name", "kMDItemWhereFroms", "-name", "kMDItemDateAdded", "-name", "kMDItemLastUsedDate")
	wg.Wait()

	if f.Signature.TeamID != "" {
		f.App.TeamID = f.Signature.TeamID
	}
	if fi, err := os.Stat(filepath.Join(target, "Contents", "_MASReceipt", "receipt")); err == nil && fi.Mode().IsRegular() {
		f.AppStore = true
	}
	var ok bool
	if f.Plist, ok = readPlistFacts(target); !ok {
		unavailable = append(unavailable, "plist")
	}
	f.LaunchItems = launchItems(env, a.BundleID)
	sort.Strings(unavailable)
	f.Unavailable = unavailable
	return f
}

// launchItems lists the .plist files in the user and system LaunchAgents
// folders and the system LaunchDaemons folder whose name starts with
// bundleID, sorted. A plain prefix match: a sibling id sharing the prefix
// is rare and worth showing. No bundle id means nothing to match.
func launchItems(env fsx.Env, bundleID string) []string {
	if bundleID == "" {
		return nil
	}
	dirs := []string{
		env.User("Library", "LaunchAgents"),
		env.Sys("Library", "LaunchAgents"),
		env.Sys("Library", "LaunchDaemons"),
	}
	var out []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasSuffix(name, ".plist") && strings.HasPrefix(name, bundleID) {
				out = append(out, filepath.Join(dir, name))
			}
		}
	}
	sort.Strings(out)
	return out
}
