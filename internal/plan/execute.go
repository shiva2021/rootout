package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shiva2021/rootout/internal/brew"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/priv"
	"github.com/shiva2021/rootout/internal/run"
	"github.com/shiva2021/rootout/internal/trash"
)

// Deps are the side-effecting collaborators Execute uses; tests inject fakes.
type Deps struct {
	Trash     *trash.Mover
	Brew      *brew.Brew
	Priv      priv.Options
	Run       run.Runner         // quit app, user launchctl bootout
	RemoveAll func(string) error // permanent user-level deletion; default os.RemoveAll
	UID       int                // for launchctl gui/<uid>; default os.Getuid()
	Dotfiles  bool               // mirrors Options.Dotfiles for re-validation
}

// Result is what actually happened.
type Result struct {
	Removed    []string            `json:"removed"`
	Skipped    []string            `json:"skipped,omitempty"` // already gone
	Failed     map[string]string   `json:"failed,omitempty"`
	BootedOut  []string            `json:"booted_out,omitempty"`
	Forgotten  []string            `json:"forgotten,omitempty"`
	Reported   []leftover.Leftover `json:"reported,omitempty"` // login items, left for the user
	BrewZapped bool                `json:"brew_zapped"`
	BrewError  string              `json:"brew_error,omitempty"`
	AdminUsed  bool                `json:"admin_used"`
}

// OK reports whether every selected path was handled.
func (r *Result) OK() bool { return len(r.Failed) == 0 && r.BrewError == "" }

var bundleIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Execute carries out p. Order: quit the app, boot out user launch agents,
// brew zap (if chosen), user-owned paths (Trash or permanent), then one admin
// elevation for root-owned paths, system launchd jobs and receipts. Every
// selected path is re-validated first; a violation aborts before any change.
func Execute(ctx context.Context, env fsx.Env, p *Plan, d Deps) (*Result, error) {
	if p.Mode == DryRun {
		return nil, ErrDryRun
	}
	if err := p.Validate(env, d.Dotfiles); err != nil {
		return nil, err
	}
	d = d.withDefaults()
	res := &Result{Failed: map[string]string{}}

	sel := p.Selected()
	var userPaths, rootPaths, userLabels, rootLabels, pkgs []string
	for _, it := range sel {
		switch {
		case it.Kind == leftover.LoginItem:
			res.Reported = append(res.Reported, it.Leftover)
		case it.Kind == leftover.Receipt:
			pkgs = append(pkgs, strings.TrimPrefix(it.Path, "pkg:"))
		case it.NeedsRoot:
			rootPaths = append(rootPaths, it.Path)
			if it.Kind == leftover.LaunchAgent || it.Kind == leftover.LaunchDaemon {
				if l := launchdLabel(it.Path); l != "" {
					rootLabels = append(rootLabels, l)
				}
			}
		default:
			userPaths = append(userPaths, it.Path)
			if it.Kind == leftover.LaunchAgent {
				if l := launchdLabel(it.Path); l != "" {
					userLabels = append(userLabels, l)
				}
			}
		}
	}

	quitApp(ctx, d.Run, p.App.BundleID)
	for _, l := range userLabels {
		if _, err := d.Run(ctx, "launchctl", "bootout", "gui/"+strconv.Itoa(d.UID)+"/"+l); err == nil {
			res.BootedOut = append(res.BootedOut, l)
		}
	}

	if p.BrewZap && p.App.Cask != "" {
		if err := d.Brew.Zap(ctx, p.App.Cask); err != nil {
			res.BrewError = err.Error()
		} else {
			res.BrewZapped = true
		}
	}

	switch p.Mode {
	case Trash:
		tr, err := d.Trash.Move(ctx, userPaths)
		res.Removed = append(res.Removed, tr.Moved...)
		res.Skipped = append(res.Skipped, tr.Skipped...)
		for path, e := range tr.Failed {
			res.Failed[path] = e.Error()
		}
		if err != nil && !errors.Is(err, trash.ErrAllFailed) {
			return res, err
		}
	case Permanent:
		for _, path := range userPaths {
			if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
				res.Skipped = append(res.Skipped, path)
				continue
			}
			if err := d.RemoveAll(path); err != nil {
				res.Failed[path] = err.Error()
			} else {
				res.Removed = append(res.Removed, path)
			}
		}
	}

	if len(rootPaths)+len(rootLabels)+len(pkgs) > 0 {
		var cmds []priv.Cmd
		for _, l := range rootLabels {
			cmds = append(cmds, priv.Cmd{"launchctl", "bootout", "system/" + l})
		}
		if len(rootPaths) > 0 {
			cmds = append(cmds, append(priv.Cmd{"/bin/rm", "-rf", "--"}, rootPaths...))
		}
		for _, id := range pkgs {
			if bundleIDRe.MatchString(id) {
				cmds = append(cmds, priv.Cmd{"pkgutil", "--forget", id})
			}
		}
		res.AdminUsed = true
		if err := priv.Exec(ctx, cmds, d.Priv); err != nil {
			for _, path := range rootPaths {
				res.Failed[path] = err.Error()
			}
			if errors.Is(err, priv.ErrCancelled) {
				return res, err
			}
		} else {
			res.Removed = append(res.Removed, rootPaths...)
			res.BootedOut = append(res.BootedOut, rootLabels...)
			res.Forgotten = append(res.Forgotten, pkgs...)
		}
	}
	return res, nil
}

// quitApp asks the app to quit gracefully; errors are ignored because the app
// is usually not running. The bundle id is validated before it reaches AppleScript.
func quitApp(ctx context.Context, r run.Runner, bundleID string) {
	if bundleID == "" || !bundleIDRe.MatchString(bundleID) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _ = r(ctx, "osascript", "-e", fmt.Sprintf(`tell application id "%s" to if it is running then quit`, bundleID))
}

func (d Deps) withDefaults() Deps {
	if d.Trash == nil {
		d.Trash = trash.New()
	}
	if d.Brew == nil {
		d.Brew = brew.New()
	}
	if d.Run == nil {
		d.Run = run.Default
	}
	if d.RemoveAll == nil {
		d.RemoveAll = os.RemoveAll
	}
	if d.UID == 0 {
		d.UID = os.Getuid()
	}
	return d
}
