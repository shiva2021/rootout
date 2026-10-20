package heur

// Rule group C: manifest and identity indicators. These are extension-level
// rules that look at package.json metadata, the file list and (when --online
// was used) registry trust signals. Research indicators 6-10.

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func init() {
	RegisterExt(ExtRule{Name: "install_hook", Check: metaInstallHook})
	RegisterExt(ExtRule{Name: "activation_star", Check: metaActivationStar})
	RegisterExt(ExtRule{Name: "activation_startup", Check: metaActivationStartup})
	RegisterExt(ExtRule{Name: "typosquat", Check: metaTyposquat})
	RegisterExt(ExtRule{Name: "impersonation", Check: metaImpersonation})
	RegisterExt(ExtRule{Name: "repo_mismatch", Check: metaRepoMismatch})
	RegisterExt(ExtRule{Name: "native_binary", Check: metaNativeBinary})
	RegisterExt(ExtRule{Name: "unsigned_gallery", Check: metaUnsignedGallery})
	RegisterExt(ExtRule{Name: "marketplace_trust", Check: metaMarketplaceTrust})
	RegisterExt(ExtRule{Name: "theme_with_code", Check: metaThemeWithCode})
}

// ---- install_hook -----------------------------------------------------------

// npm lifecycle scripts ship in package.json but VS Code does not run them
// when it installs an extension from a VSIX; they matter only as evidence of
// what the package does. `vscode:uninstall` is the one hook VS Code runs: a
// Node script executed after the extension is uninstalled.
var metaNPMHooks = []string{"preinstall", "install", "postinstall", "prepare"}

const metaUninstallHook = "vscode:uninstall"

var metaHookDangerous = []string{"curl", "wget", "node -e", "base64", "powershell", "bash -c", "sh -c", "eval", "http://", "https://"}

func metaHookIsDangerous(script string) bool {
	lower := strings.ToLower(script)
	for _, tok := range metaHookDangerous {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

func metaInstallHook(c *ExtCtx) []Finding {
	var out []Finding
	for _, hook := range metaNPMHooks {
		script := strings.TrimSpace(c.Ext.Scripts[hook])
		if script == "" {
			continue
		}
		sev, why := Low, "build-time npm script (VS Code does not run it on install)"
		if metaHookIsDangerous(script) {
			sev, why = High, "npm script downloads or decodes code (not run by VS Code on install, but shows intent)"
		}
		out = append(out, Finding{
			Rule:     "install_hook",
			Severity: sev,
			Detail:   fmt.Sprintf("package.json %q: %s: %s", hook, why, metaTruncate(script, 160)),
		})
	}
	if script := strings.TrimSpace(c.Ext.Scripts[metaUninstallHook]); script != "" {
		sev := Medium
		if metaHookIsDangerous(script) {
			sev = Critical
		}
		out = append(out, Finding{
			Rule:     "install_hook",
			Severity: sev,
			Detail:   fmt.Sprintf("package.json %q runs a Node script after uninstall: %s", metaUninstallHook, metaTruncate(script, 160)),
		})
	}
	return out
}

// ---- activation -------------------------------------------------------------

// metaStartupJustified lists contribution points that plausibly need the
// extension to be running from startup (task providers, debuggers, terminal
// integrations, language servers, auth providers).
var metaStartupJustified = map[string]bool{
	"taskDefinitions": true, "debuggers": true, "terminal": true, "languages": true, "authentication": true,
}

func metaActivationStar(c *ExtCtx) []Finding {
	for _, ev := range c.Ext.ActivationEvents {
		if strings.TrimSpace(ev) == "*" {
			return []Finding{{
				Rule:     "activation_star",
				Severity: Medium,
				Detail:   `activationEvents contains "*": the extension loads on every editor start regardless of workspace`,
			}}
		}
	}
	return nil
}

func metaActivationStartup(c *ExtCtx) []Finding {
	has := false
	for _, ev := range c.Ext.ActivationEvents {
		switch strings.TrimSpace(ev) {
		case "*":
			return nil // activation_star already covers it
		case "onStartupFinished":
			has = true
		}
	}
	if !has {
		return nil
	}
	onlyCommands := true
	for _, k := range c.Ext.Contributes {
		if metaStartupJustified[k] {
			return nil
		}
		if k != "commands" {
			onlyCommands = false
		}
	}
	sev := Low
	detail := "activates onStartupFinished but contributes nothing that needs to run at startup"
	if onlyCommands {
		sev = Medium
		detail = "activates onStartupFinished while contributing only commands (or nothing): startup activation is unexplained"
	}
	return []Finding{{Rule: "activation_startup", Severity: sev, Detail: detail}}
}

// ---- typosquat --------------------------------------------------------------

// metaTypoDistance is the maximum edit distance that counts as a lookalike.
const metaTypoDistance = 2

// metaMinPublisherLen guards the publisher-only comparison: short ids are
// within distance 2 of too many things.
const metaMinPublisherLen = 6

// metaMinDisplayLen skips generic one-word display names ("Go", "Vim", "C#")
// in the display-name comparison.
const metaMinDisplayLen = 4

func metaTyposquat(c *ExtCtx) []Finding {
	id, pub := metaID(c)
	if id == "" || metaPopularExact(id) {
		return nil
	}
	var out []Finding
	idTargets := map[string]bool{} // popular ids already reported
	pubTargets := map[string]bool{}
	display := strings.ToLower(strings.TrimSpace(c.Ext.DisplayName))
	for _, p := range popularExtensions {
		ppub := p.publisher()
		if d := metaDistance(id, p.id); d > 0 && d <= metaTypoDistance && !idTargets[p.id] {
			idTargets[p.id] = true
			pubTargets[ppub] = true
			out = append(out, Finding{
				Rule:     "typosquat",
				Severity: High,
				Detail:   fmt.Sprintf("extension id %q is %d edit(s) away from popular %q", id, d, p.id),
			})
		}
		if pub != ppub && len(pub) >= metaMinPublisherLen && !pubTargets[ppub] {
			if d := metaDistance(pub, ppub); d > 0 && d <= metaTypoDistance {
				pubTargets[ppub] = true
				out = append(out, Finding{
					Rule:     "typosquat",
					Severity: High,
					Detail:   fmt.Sprintf("publisher %q is %d edit(s) away from popular publisher %q (%s)", pub, d, ppub, p.id),
				})
			}
		}
		if pub != ppub && display != "" && len(p.displayName) >= metaMinDisplayLen && display == strings.ToLower(p.displayName) && !idTargets[p.id] {
			idTargets[p.id] = true
			out = append(out, Finding{
				Rule:     "typosquat",
				Severity: High,
				Detail:   fmt.Sprintf("display name %q is the name of popular %q but the publisher is %q", c.Ext.DisplayName, p.id, pub),
			})
		}
	}
	return out
}

// ---- impersonation ----------------------------------------------------------

// metaBrands maps a brand word to the publishers allowed to use it. A "ms-"
// prefix stands for every Microsoft publisher.
var metaBrands = []struct {
	word       string
	publishers []string
}{
	{"Microsoft", []string{"microsoft", "ms-"}},
	{"GitHub", []string{"github"}},
	{"Google", []string{"google", "googlecloudtools"}},
	{"Amazon", []string{"amazonwebservices"}},
	{"OpenAI", []string{"openai"}},
	{"Anthropic", []string{"anthropic"}},
	{"JetBrains", []string{"jetbrains"}},
	{"Official", nil},
}

func metaImpersonation(c *ExtCtx) []Finding {
	id, pub := metaID(c)
	if pub == "" || metaPopularExact(id) {
		return nil
	}
	var out []Finding
	for _, b := range metaBrands {
		if metaPublisherAllowed(pub, b.publishers) {
			continue
		}
		if b.word == "Official" && TrustedPublishers[pub] {
			continue
		}
		switch {
		case metaWholeWord(c.Ext.DisplayName, b.word):
			out = append(out, Finding{
				Rule:     "impersonation",
				Severity: Medium,
				Detail:   fmt.Sprintf("display name %q uses %q but the publisher is %q", c.Ext.DisplayName, b.word, pub),
			})
		case metaWholeWord(c.Ext.Description, b.word):
			// Brand mentions in descriptions are common in legitimate
			// integrations, so they weigh less than a branded display name.
			out = append(out, Finding{
				Rule:     "impersonation",
				Severity: Low,
				Detail:   fmt.Sprintf("description mentions %q but the publisher is %q", b.word, pub),
			})
		}
	}
	return out
}

func metaPublisherAllowed(pub string, allowed []string) bool {
	for _, a := range allowed {
		if strings.HasSuffix(a, "-") {
			if strings.HasPrefix(pub, a) {
				return true
			}
		} else if pub == a {
			return true
		}
	}
	return false
}

// ---- repo_mismatch ----------------------------------------------------------

func metaRepoMismatch(c *ExtCtx) []Finding {
	id, pub := metaID(c)
	owner := metaRepoOwner(c.Ext.Repository)
	if owner == "" || pub == "" || metaOwnerMatches(owner, pub) {
		return nil
	}
	for _, p := range popularExtensions {
		if !metaOwnerMatches(owner, p.publisher()) {
			continue
		}
		if d := metaDistance(id, p.id); d > 0 && d <= metaTypoDistance {
			return []Finding{{
				Rule:     "repo_mismatch",
				Severity: Medium,
				Detail:   fmt.Sprintf("repository %s belongs to %q, the publisher of popular %q, but this extension is published by %q", c.Ext.Repository, owner, p.id, pub),
			}}
		}
	}
	return []Finding{{
		Rule:     "repo_mismatch",
		Severity: Low,
		Detail:   fmt.Sprintf("repository owner %q differs from publisher %q", owner, pub),
	}}
}

// metaOwnerMatches reports whether a repository owner plausibly is the
// publisher. Microsoft publishes from github.com/microsoft under the "ms-*"
// ids seen in the popular list; an arbitrary "ms-foo" does not qualify.
func metaOwnerMatches(owner, pub string) bool {
	if owner == pub {
		return true
	}
	return owner == "microsoft" && (pub == "microsoft" || (strings.HasPrefix(pub, "ms-") && metaPopularPublisher(pub)))
}

// metaRepoOwner extracts the lower-cased first path segment of a GitHub or
// GitLab repository URL ("https://github.com/Owner/repo.git" -> "owner").
// It understands the git+https, ssh and "github:owner/repo" spellings.
func metaRepoOwner(repo string) string {
	r := strings.ToLower(strings.TrimSpace(repo))
	if r == "" {
		return ""
	}
	r = strings.TrimPrefix(r, "git+")
	for _, host := range []string{"github.com", "gitlab.com"} {
		i := strings.Index(r, host)
		if i < 0 {
			continue
		}
		rest := strings.TrimLeft(r[i+len(host):], ":/")
		seg, _, _ := strings.Cut(rest, "/")
		return seg
	}
	for _, short := range []string{"github:", "gitlab:"} {
		if strings.HasPrefix(r, short) {
			seg, _, _ := strings.Cut(r[len(short):], "/")
			return seg
		}
	}
	return ""
}

// ---- native_binary ----------------------------------------------------------

var metaBinaryExts = map[string]bool{".node": true, ".dylib": true, ".so": true, ".dll": true, ".exe": true}

func metaNativeBinary(c *ExtCtx) []Finding {
	var outside, inside []string
	for _, f := range c.Files {
		if !metaBinaryExts[strings.ToLower(path.Ext(f))] {
			continue
		}
		if metaInNodeModules(f) {
			inside = append(inside, f)
		} else {
			outside = append(outside, f)
		}
	}
	if len(outside)+len(inside) == 0 {
		return nil
	}
	nonNative := metaNonNativePurpose(c)
	var out []Finding
	switch {
	case nonNative && len(outside) > 0:
		out = append(out, Finding{
			Rule:     "native_binary",
			Severity: High,
			File:     outside[0],
			Detail:   "native binary shipped by an extension whose declared purpose (theme/snippets/keymap) needs no native code: " + metaListFiles(outside),
		})
		if len(inside) > 0 {
			out = append(out, Finding{
				Rule:     "native_binary",
				Severity: Medium,
				File:     inside[0],
				Detail:   "native binaries under node_modules in a theme/snippets/keymap extension: " + metaListFiles(inside),
			})
		}
	case nonNative:
		out = append(out, Finding{
			Rule:     "native_binary",
			Severity: Medium,
			File:     inside[0],
			Detail:   "native binaries under node_modules in a theme/snippets/keymap extension: " + metaListFiles(inside),
		})
	default:
		all := append(append([]string{}, outside...), inside...)
		out = append(out, Finding{
			Rule:     "native_binary_present",
			Severity: Info,
			File:     all[0],
			Detail:   "extension ships native binaries: " + metaListFiles(all),
		})
	}
	return out
}

// metaNonNativePurpose is true for extensions whose manifest says they are
// themes, snippets or keymaps: nothing about that needs native code.
func metaNonNativePurpose(c *ExtCtx) bool {
	for _, cat := range c.Ext.Categories {
		switch strings.ToLower(cat) {
		case "themes", "snippets", "keymaps":
			return true
		}
	}
	return metaContributesOnly(c.Ext.Contributes, map[string]bool{
		"themes": true, "iconThemes": true, "productIconThemes": true, "snippets": true,
	})
}

// metaContributesOnly reports whether contributes is non-empty and a subset of allowed.
func metaContributesOnly(contributes []string, allowed map[string]bool) bool {
	if len(contributes) == 0 {
		return false
	}
	for _, k := range contributes {
		if !allowed[k] {
			return false
		}
	}
	return true
}

// ---- unsigned_gallery -------------------------------------------------------

func metaUnsignedGallery(c *ExtCtx) []Finding {
	if c.Ext.Source != "gallery" || c.Ext.Signed {
		return nil
	}
	return []Finding{{
		Rule:     "unsigned_gallery",
		Severity: Low,
		Detail:   "installed from the gallery but no marketplace signature file was found; signature file naming varies between VS Code versions, so this is weak evidence",
	}}
}

// ---- marketplace_trust ------------------------------------------------------

const (
	metaNewPublisherDays = 180
	metaLowInstalls      = 100
)

func metaMarketplaceTrust(c *ExtCtx) []Finding {
	m := c.Marketplace
	if m == nil {
		return nil
	}
	var out []Finding
	if !m.Verified {
		out = append(out, Finding{Rule: "unverified_publisher", Severity: Medium, Detail: "publisher is not domain-verified on the registry"})
	}
	if m.PublisherAge < metaNewPublisherDays {
		out = append(out, Finding{Rule: "new_publisher", Severity: Low, Detail: fmt.Sprintf("publisher account is %d days old (under %d)", m.PublisherAge, metaNewPublisherDays)})
	}
	if m.Installs < metaLowInstalls {
		out = append(out, Finding{Rule: "low_installs", Severity: Low, Detail: fmt.Sprintf("only %d installs reported (under %d)", m.Installs, metaLowInstalls)})
	}
	if !m.HasRepository {
		out = append(out, Finding{Rule: "no_repository_online", Severity: Info, Detail: "registry listing has no repository link"})
	}
	if m.Source == "openvsx" && !m.Trusted {
		out = append(out, Finding{Rule: "untrusted_publishing", Severity: Low, Detail: "Open VSX listing was not published with trusted publishing"})
	}
	return out
}

// ---- theme_with_code --------------------------------------------------------

var metaThemeContributes = map[string]bool{"themes": true, "iconThemes": true, "productIconThemes": true}

func metaThemeWithCode(c *ExtCtx) []Finding {
	if !metaIsTheme(c) {
		return nil
	}
	if c.Ext.Main != "" {
		return []Finding{{
			Rule:     "theme_with_code",
			Severity: High,
			File:     c.Ext.Main,
			Detail:   fmt.Sprintf("theme extension declares an entry point %q; a theme needs no code", c.Ext.Main),
		}}
	}
	var js []string
	for _, f := range c.Files {
		if metaInNodeModules(f) {
			continue
		}
		switch strings.ToLower(path.Ext(f)) {
		case ".js", ".cjs", ".mjs":
			js = append(js, f)
		}
	}
	if len(js) == 0 {
		return nil
	}
	return []Finding{{
		Rule:     "theme_with_code",
		Severity: High,
		File:     js[0],
		Detail:   "theme extension ships JavaScript outside node_modules; a theme needs no code: " + metaListFiles(js),
	}}
}

func metaIsTheme(c *ExtCtx) bool {
	for _, cat := range c.Ext.Categories {
		if strings.EqualFold(cat, "Themes") {
			return true
		}
	}
	return metaContributesOnly(c.Ext.Contributes, metaThemeContributes)
}

// ---- helpers ----------------------------------------------------------------

// metaID returns the lower-cased "publisher.name" id and publisher of the
// extension, preferring the manifest fields over the pre-computed ID.
func metaID(c *ExtCtx) (id, pub string) {
	pub = strings.ToLower(strings.TrimSpace(c.Ext.Publisher))
	name := strings.ToLower(strings.TrimSpace(c.Ext.Name))
	if pub != "" && name != "" {
		return pub + "." + name, pub
	}
	id = strings.ToLower(strings.TrimSpace(c.Ext.ID))
	if pub == "" {
		pub, _, _ = strings.Cut(id, ".")
	}
	return id, pub
}

// metaPopularPublisher reports whether pub publishes something on the popular list.
func metaPopularPublisher(pub string) bool {
	for _, p := range popularExtensions {
		if p.publisher() == pub {
			return true
		}
	}
	return false
}

func metaPopularExact(id string) bool {
	for _, p := range popularExtensions {
		if p.id == id {
			return true
		}
	}
	return false
}

func metaInNodeModules(rel string) bool {
	return strings.HasPrefix(rel, "node_modules/") || strings.Contains(rel, "/node_modules/")
}

// metaListFiles renders up to three file names plus a count.
func metaListFiles(files []string) string {
	sorted := append([]string{}, files...)
	sort.Strings(sorted)
	if len(sorted) <= 3 {
		return strings.Join(sorted, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(sorted[:3], ", "), len(sorted)-3)
}

// metaTruncate cuts s to at most n runes, marking the cut with an ellipsis.
func metaTruncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// metaWholeWord reports whether word occurs in s as a whole word,
// case-insensitively. Word boundaries are any non letter/digit rune.
func metaWholeWord(s, word string) bool {
	ls, lw := strings.ToLower(s), strings.ToLower(word)
	if lw == "" {
		return false
	}
	for start := 0; start < len(ls); {
		i := strings.Index(ls[start:], lw)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(lw)
		before, _ := utf8.DecodeLastRuneInString(ls[:i])
		after, _ := utf8.DecodeRuneInString(ls[end:])
		if !metaIsWordRune(before) && !metaIsWordRune(after) {
			return true
		}
		start = i + 1
	}
	return false
}

func metaIsWordRune(r rune) bool {
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// metaDistance is the optimal-string-alignment (restricted Damerau-Levenshtein)
// distance between a and b, counting insertions, deletions, substitutions and
// adjacent transpositions as one edit each.
func metaDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev2 := make([]int, lb+1)
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[lb]
}
