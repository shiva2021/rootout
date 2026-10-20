// Package safety is the single choke point every deletion passes through.
// Validate decides whether a path may be deleted at all, CheckApp decides
// whether an application bundle is protected, and NameOK decides whether an
// app name is specific enough to drive name-based leftover matching.
package safety

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/shiva2021/purge/internal/fsx"
)

// Options widens the allowlist for explicit opt-ins.
type Options struct {
	AllowDotfiles bool // permits ~/.config/<x> and ~/.<x> (flag --dotfiles)
}

// Violation explains why a path was rejected. Rule is a stable snake_case id:
// "not_absolute", "relative_component", "outside_allowlist", "protected_prefix",
// "allowed_prefix_itself", "library_top_level", "too_shallow", "symlink_escape",
// and for CheckApp "apple_bundle_id", "system_path", "sip_restricted".
type Violation struct {
	Path   string
	Rule   string
	Detail string
}

func (v *Violation) Error() string {
	return fmt.Sprintf("safety: refusing %s: %s: %s", v.Path, v.Rule, v.Detail)
}

func violation(path, rule, detail string) error {
	return &Violation{Path: path, Rule: rule, Detail: detail}
}

// Validate rejects any path that must never be deleted. It does not require
// the path to exist. See docs/kb/safety.md for the full rule set.
func Validate(env fsx.Env, path string, opts Options) error {
	if !filepath.IsAbs(path) {
		return violation(path, "not_absolute", "path must be absolute")
	}
	for _, c := range strings.Split(path, string(filepath.Separator)) {
		if c == ".." {
			return violation(path, "relative_component", "path contains a .. component")
		}
	}
	p := filepath.Clean(path)

	for _, pre := range protectedPrefixes(env) {
		if _, ok := within(pre, p); ok {
			return violation(p, "protected_prefix", "under protected prefix "+pre)
		}
	}
	for _, pre := range protectedExact(env) {
		if p == pre {
			return violation(p, "protected_prefix", "protected directory "+pre)
		}
	}

	prefix, rel, dotfile, err := allowedPrefix(env, p, opts)
	if err != nil {
		return err
	}
	if rel == "." {
		return violation(p, "allowed_prefix_itself", "is the allowed prefix "+prefix)
	}
	depth := len(strings.Split(rel, string(filepath.Separator)))

	if (prefix == env.Library() || prefix == env.Sys("Library")) && depth == 1 {
		return violation(p, "library_top_level", "direct child of "+prefix)
	}
	if !dotfile && env.UnderHome(p) {
		homeRel, _ := within(env.Home, p)
		homeDepth := len(strings.Split(homeRel, string(filepath.Separator)))
		isApp := prefix == env.User("Applications") && homeDepth == 2 && strings.HasSuffix(p, ".app")
		if homeDepth < 3 && !isApp {
			return violation(p, "too_shallow", fmt.Sprintf("only %d components below home, need 3", homeDepth))
		}
	}
	return checkSymlinks(prefix, rel, p)
}

// ValidateAll returns the first violation or nil.
func ValidateAll(env fsx.Env, paths []string, opts Options) error {
	for _, p := range paths {
		if err := Validate(env, p, opts); err != nil {
			return err
		}
	}
	return nil
}

// allowedPrefix finds the allowed prefix p lies under. It returns the prefix,
// p relative to it, and whether the match came through the dotfile opt-in
// (dotfile targets are exempt from the depth rule: ~/.<x> is a valid target).
func allowedPrefix(env fsx.Env, p string, opts Options) (prefix, rel string, dotfile bool, err error) {
	for _, pre := range allowedPrefixes(env) {
		if r, ok := within(pre, p); ok {
			return pre, r, false, nil
		}
	}
	if opts.AllowDotfiles {
		if r, ok := within(env.Home, p); ok && r != "." {
			first := strings.Split(r, string(filepath.Separator))[0]
			if strings.HasPrefix(first, ".") && len(first) > 1 {
				if first == ".config" {
					pre := env.User(".config")
					r, _ = within(pre, p)
					return pre, r, true, nil
				}
				return env.Home, r, true, nil
			}
		}
	}
	return "", "", false, violation(p, "outside_allowlist", "not under an allowed prefix")
}

// checkSymlinks walks the components between prefix and p (excluding p itself)
// and rejects if any of them is a symlink that resolves outside prefix. The
// final component may be a symlink: deleting it removes the link, not the target.
func checkSymlinks(prefix, rel, p string) error {
	comps := strings.Split(rel, string(filepath.Separator))
	if len(comps) < 2 {
		return nil
	}
	resolvedPrefix, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("safety: resolve %s: %w", prefix, err)
	}
	cur := prefix
	for _, c := range comps[:len(comps)-1] {
		cur = filepath.Join(cur, c)
		fi, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("safety: lstat %s: %w", cur, err)
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			continue
		}
		target, err := filepath.EvalSymlinks(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return violation(p, "symlink_escape", "parent "+cur+" is a dangling symlink")
			}
			return fmt.Errorf("safety: resolve %s: %w", cur, err)
		}
		if _, ok := within(resolvedPrefix, target); !ok {
			return violation(p, "symlink_escape", "parent "+cur+" resolves to "+target+" outside "+prefix)
		}
	}
	return nil
}

// within reports whether p is prefix or lies under it; rel is p relative to
// prefix ("." when equal). Both must be cleaned absolute paths.
func within(prefix, p string) (string, bool) {
	rel, err := filepath.Rel(prefix, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// CheckApp rejects protected applications: a bundle id with prefix
// "com.apple.", a path under /System, or a bundle carrying the SF_RESTRICTED
// (SIP) flag. Returns a *Violation with Rule "apple_bundle_id", "system_path"
// or "sip_restricted".
func CheckApp(env fsx.Env, appPath, bundleID string) error {
	p := filepath.Clean(appPath)
	if strings.HasPrefix(bundleID, "com.apple.") {
		return violation(p, "apple_bundle_id", "bundle id "+bundleID+" belongs to Apple")
	}
	if _, ok := within(env.Sys("System"), p); ok {
		return violation(p, "system_path", "under "+env.Sys("System"))
	}
	restricted, err := isRestricted(p)
	if err != nil {
		return fmt.Errorf("safety: stat %s: %w", p, err)
	}
	if restricted {
		return violation(p, "sip_restricted", "carries the SF_RESTRICTED flag")
	}
	return nil
}

// NameOK reports whether an app name may be used for name-based leftover
// matching: at least 4 runes, not in the denylist (case-insensitive), and
// corroborated by the bundle id. Corroboration: the normalized name (lowercase,
// spaces/dashes/underscores removed) appears in the normalized bundle id, or
// the normalized last segment of the bundle id appears in the normalized name.
// Names that fail this (e.g. "Visual Studio Code" / com.microsoft.VSCode) can
// be matched explicitly with --also.
func NameOK(name, bundleID string) bool {
	if bundleID == "" || utf8.RuneCountInString(name) < 4 {
		return false
	}
	if _, denied := denied[strings.ToLower(name)]; denied {
		return false
	}
	n := normalize(name)
	if n == "" {
		return false
	}
	if strings.Contains(normalize(bundleID), n) {
		return true
	}
	// A short last segment ("id", "app", "x") is too weak to corroborate a name.
	last := bundleID[strings.LastIndex(bundleID, ".")+1:]
	seg := normalize(last)
	return utf8.RuneCountInString(seg) >= 3 && strings.Contains(n, seg)
}

// normalize lowercases s and strips spaces, dashes and underscores.
func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '_':
			return -1
		}
		return r
	}, strings.ToLower(s))
}
