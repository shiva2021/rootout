package heur

// Rule group D: hygiene. These rules encode what the manual audit in
// docs/research/vscode-extension-audit/REPORT.md actually found: orphaned
// folders left after uninstall, stale extensions bundling vulnerable
// dependencies, plaintext session tokens written under ~, and AI-agent
// wrappers that launch tools with their permission prompts disabled.

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// hygNow is the clock used by stale_extension; tests override it.
var hygNow = time.Now

// hygStaleAfter is how old an extension may be before it counts as stale.
const hygStaleAfter = 2 * 365 * 24 * time.Hour

func init() {
	RegisterExt(ExtRule{Name: "orphaned_folder", Check: hygOrphanedFolder})
	RegisterExt(ExtRule{Name: "stale_extension", Check: hygStaleExtension})
	RegisterExt(ExtRule{Name: "vulnerable_bundled_dep", Check: hygVulnerableBundledDep})
	RegisterExt(ExtRule{Name: "unknown_source", Check: hygUnknownSource})

	RegisterFile(FileRule{Name: "permission_bypass_launch", Check: hygPermissionBypassLaunch})
	RegisterFile(FileRule{Name: "plaintext_token_write", Check: hygPlaintextTokenWrite})
	RegisterFile(FileRule{Name: "settings_tamper", Check: hygSettingsTamper})
	RegisterFile(FileRule{Name: "agent_config_write", Check: hygAgentConfigWrite})
}

// ---- extension rules -------------------------------------------------------

func hygOrphanedFolder(c *ExtCtx) []Finding {
	if !c.Ext.Orphaned {
		return nil
	}
	sev := Medium
	detail := "folder remains on disk after uninstall; the editor no longer loads it, but its files (and any stored credentials) are still present"
	if c.Ext.HasNodeModules {
		sev = High
		detail += "; it still bundles node_modules"
	}
	return []Finding{{Rule: "orphaned_folder", Severity: sev, Detail: detail}}
}

func hygStaleExtension(c *ExtCtx) []Finding {
	if c.Ext.UpdatedAt.IsZero() {
		return nil
	}
	age := hygNow().Sub(c.Ext.UpdatedAt)
	if age <= hygStaleAfter {
		return nil
	}
	sev := Low
	days := int(age.Hours() / 24)
	detail := fmt.Sprintf("last updated %d days ago (%s); it will not have picked up fixes since", days, c.Ext.UpdatedAt.Format("2006-01-02"))
	if c.Ext.HasNodeModules {
		sev = Medium
		detail += " and it bundles node_modules of the same age"
	}
	return []Finding{{Rule: "stale_extension", Severity: sev, Detail: detail}}
}

func hygVulnerableBundledDep(c *ExtCtx) []Finding {
	names := make([]string, 0, len(c.Ext.Dependencies))
	for n := range c.Ext.Dependencies {
		names = append(names, n)
	}
	sort.Strings(names)

	var out []Finding
	for _, n := range names {
		ver := c.Ext.Dependencies[n]
		for _, a := range hygVulnerable(n, ver) {
			detail := fmt.Sprintf("%s@%s < %s (%s)", n, ver, a.fixedIn, a.advisory)
			if a.fixedIn == "" {
				detail = fmt.Sprintf("%s@%s deprecated, no fixed version (%s)", n, ver, a.advisory)
			}
			out = append(out, Finding{Rule: "vulnerable_bundled_dep", Severity: Medium, Detail: detail})
		}
	}
	if len(out) >= 3 {
		for i := range out {
			out[i].Severity = High
		}
	}
	return out
}

func hygUnknownSource(c *ExtCtx) []Finding {
	src := c.Ext.Source
	if src == "" || src == "gallery" {
		return nil
	}
	return []Finding{{
		Rule:     "unknown_source",
		Severity: Info,
		Detail:   fmt.Sprintf("sideloaded extension (source %q); it did not come through the marketplace, worth a look", src),
	}}
}

// ---- file rules ------------------------------------------------------------

// hygCodeExts are the files the hygiene file rules look at.
var hygCodeExts = map[string]bool{
	".js": true, ".cjs": true, ".mjs": true, ".jsx": true,
	".ts": true, ".tsx": true, ".py": true,
}

func hygIsCode(rel string) bool {
	return hygCodeExts[strings.ToLower(path.Ext(rel))]
}

// hygWindow returns data[off-before : off+after], clamped.
func hygWindow(data []byte, off, before, after int) []byte {
	lo := off - before
	if lo < 0 {
		lo = 0
	}
	hi := off + after
	if hi > len(data) {
		hi = len(data)
	}
	return data[lo:hi]
}

// hygArgs returns the byte range of the argument list that starts at the
// open paren at off (exclusive of the parens), scanning at most limit bytes.
// It is a rough balance count; it does not understand strings.
func hygArgs(data []byte, off, limit int) (start, end int) {
	start = off + 1
	depth := 0
	for i := off; i < len(data) && i-off < limit; i++ {
		switch data[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return start, i
			}
		}
	}
	end = off + limit
	if end > len(data) {
		end = len(data)
	}
	return start, end
}

func hygStartsOnActivation(events []string) bool {
	for _, e := range events {
		if e == "onStartupFinished" || e == "*" {
			return true
		}
	}
	return false
}

// -- permission_bypass_launch

type hygBypassFlag struct {
	re   *regexp.Regexp
	near *regexp.Regexp // when set, the flag only counts within hygBypassNear bytes of a match
	span int
}

const hygBypassNear = 300

var hygBypassFlags = []hygBypassFlag{
	{re: regexp.MustCompile(`--permission-mode[ =]bypassPermissions`)},
	{re: regexp.MustCompile(`--dangerously-skip-permissions`)},
	{re: regexp.MustCompile(`--yolo\b`)},
	{re: regexp.MustCompile(`--trust-all-tools`)},
	{re: regexp.MustCompile(`--allow-all\b`)},
	{re: regexp.MustCompile(`--no-sandbox`), near: regexp.MustCompile(`\b(spawn|exec|sendText|createTerminal)\b`), span: hygBypassNear},
	{re: regexp.MustCompile(`\bnpx\s+(-y|--yes)\s`), near: regexp.MustCompile(`\b(sendText|exec|execSync)\(`), span: 200},
}

func hygPermissionBypassLaunch(c *FileCtx) []Finding {
	if !hygIsCode(c.Rel) {
		return nil
	}
	sev := High
	if hygStartsOnActivation(c.Ext.ActivationEvents) {
		sev = Critical
	}
	var out []Finding
	for _, f := range hygBypassFlags {
		for _, loc := range f.re.FindAllIndex(c.Data, -1) {
			if f.near != nil && !f.near.Match(hygWindow(c.Data, loc[0], f.span, f.span)) {
				continue
			}
			flag := strings.TrimSpace(string(c.Data[loc[0]:loc[1]]))
			out = append(out, Finding{
				Rule:     "permission_bypass_launch",
				Severity: sev,
				File:     c.Rel,
				Line:     c.Line(loc[0]),
				Snippet:  c.Snippet(loc[0], 160),
				Detail:   fmt.Sprintf("launches a tool with its safety prompts disabled (%s)", flag),
			})
			break // one finding per flag per file
		}
	}
	return out
}

// -- plaintext_token_write

var (
	hygWriteCallRe = regexp.MustCompile(`\b(?:writeFile|writeFileSync|outputFile|outputFileSync|open)\(`)
	hygPyOpenWRe   = regexp.MustCompile(`['"][wa]b?['"]`)
	hygHomeRe      = regexp.MustCompile(`homedir\(\)|\bHOME\b|\bUSERPROFILE\b|['"\x60]~[/'"\x60]`)
	// A dot-name only counts as a home dot-directory when it is a directory
	// component ('.lc/…' or '.lc', 'x'); a lone '.gitignore' is a file.
	hygDotDirRe = regexp.MustCompile(`['"\x60](\.[A-Za-z][\w-]*)(?:/|['"\x60]\s*,\s*['"\x60])`)
	// Secret names only as object keys or shorthand properties: never after
	// a dot (t.token is a property read, e.g. a cancellation token).
	hygSecretKeyRe = regexp.MustCompile(`(?i)(?:^|[^.\w$])((?:cookie|session|token|jwt|csrf|password|secret|api_?key|access_?token|refresh_?token)\w*)['"\x60]?\s*[:,}=]`)
)

// hygFileExts are dot-prefixed literals that are extensions, not dot-dirs.
var hygFileExts = map[string]bool{
	".js": true, ".cjs": true, ".mjs": true, ".ts": true, ".json": true, ".map": true, ".md": true,
	".txt": true, ".log": true, ".html": true, ".css": true, ".yml": true, ".yaml": true, ".toml": true,
	".ini": true, ".env": true, ".lock": true, ".tmp": true, ".bak": true, ".db": true, ".sqlite": true,
	".gz": true, ".zip": true, ".tar": true, ".png": true, ".svg": true, ".py": true, ".sh": true,
	".vsix": true, ".xml": true, ".csv": true, ".pem": true, ".key": true,
}

// hygUnderHome reports whether an argument list names a path under the
// user's home: an explicit home token, or a dot-directory literal.
func hygUnderHome(args []byte) bool {
	if hygHomeRe.Match(args) {
		return true
	}
	for _, m := range hygDotDirRe.FindAllSubmatch(args, -1) {
		if !hygFileExts[strings.ToLower(string(m[1]))] {
			return true
		}
	}
	return false
}

func hygPlaintextTokenWrite(c *FileCtx) []Finding {
	if !hygIsCode(c.Rel) {
		return nil
	}
	var out []Finding
	for _, loc := range hygWriteCallRe.FindAllIndex(c.Data, -1) {
		paren := loc[1] - 1
		start, end := hygArgs(c.Data, paren, 400)
		args := c.Data[start:end]
		if string(c.Data[loc[0]:loc[1]]) == "open(" && !hygPyOpenWRe.Match(args) {
			continue // open(...) without a write mode is a read
		}
		if !hygUnderHome(args) {
			continue
		}
		win := hygWindow(c.Data, loc[0], 400, end-loc[0]+400)
		keys := hygSecretKeys(win)
		if len(keys) == 0 {
			continue
		}
		out = append(out, Finding{
			Rule:     "plaintext_token_write",
			Severity: High,
			File:     c.Rel,
			Line:     c.Line(loc[0]),
			Snippet:  c.Snippet(loc[0], 160),
			Detail:   fmt.Sprintf("writes %s in plaintext to a file under the home directory", strings.Join(keys, "/")),
		})
	}
	return out
}

// hygSecretKeys returns the distinct lower-cased credential key names in b.
func hygSecretKeys(b []byte) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range hygSecretKeyRe.FindAllSubmatch(b, -1) {
		k := strings.ToLower(string(m[1]))
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}

// -- settings_tamper

var (
	hygEditorConfigRe = regexp.MustCompile(`Code/User/settings\.json|\.vscode/settings\.json|\bargv\.json|state\.vscdb|\bmcp\.json`)
	hygWriteVerbRe    = regexp.MustCompile(`\b(?:writeFile|writeFileSync|outputFile|outputFileSync|appendFile|appendFileSync|createWriteStream|copyFile|copyFileSync|rename|renameSync|truncate|unlink|unlinkSync)\(|\b(?:prepare|run|exec)\(\s*['"\x60](?:UPDATE|INSERT|DELETE|REPLACE)\b`)
	hygUpdateCallRe   = regexp.MustCompile(`\.update\(\s*['"\x60]([^'"\x60]+)['"\x60]`)
	hygGetConfigRe    = regexp.MustCompile(`getConfiguration\(\s*['"\x60]([^'"\x60]+)['"\x60]\s*\)\s*$`)
	hygAgentDirRe     = regexp.MustCompile(`['"\x60](?:[^'"\x60\n]*[/\\])?(\.claude|\.cursor|\.codeium|\.continue|\.aider[\w.-]*|\.gemini|\.copilot[\w.-]*)(?:[/\\][^'"\x60\n]*)?['"\x60]`)
)

// hygSensitiveSettings are the setting prefixes an extension has no
// business changing on the user's behalf.
var hygSensitiveSettings = []string{
	"security.workspace.trust",
	"terminal.integrated.env",
	"terminal.integrated.inheritEnv",
	"extensions.autoUpdate",
	"http.proxy",
	"telemetry",
}

func hygSettingsTamper(c *FileCtx) []Finding {
	if !hygIsCode(c.Rel) {
		return nil
	}
	var out []Finding
	seen := map[string]bool{}
	add := func(off int, detail string) {
		k := fmt.Sprintf("%d:%s", c.Line(off), detail)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, Finding{
			Rule:     "settings_tamper",
			Severity: Medium,
			File:     c.Rel,
			Line:     c.Line(off),
			Snippet:  c.Snippet(off, 160),
			Detail:   detail,
		})
	}

	for _, loc := range hygEditorConfigRe.FindAllIndex(c.Data, -1) {
		win := hygWindow(c.Data, loc[0], 300, 300)
		if !hygWriteVerbRe.Match(win) {
			continue
		}
		// ~/.claude/mcp.json and friends belong to agent_config_write.
		line := hygWindow(c.Data, loc[0], 200, 200)
		if hygAgentDirRe.Match(line) && strings.HasSuffix(string(c.Data[loc[0]:loc[1]]), "mcp.json") {
			continue
		}
		add(loc[0], fmt.Sprintf("writes the editor's own config file %s", c.Data[loc[0]:loc[1]]))
	}

	for _, m := range hygUpdateCallRe.FindAllSubmatchIndex(c.Data, -1) {
		key := string(c.Data[m[2]:m[3]])
		before := hygWindow(c.Data, m[0], 160, 0)
		if g := hygGetConfigRe.FindSubmatch(before); g != nil {
			key = string(g[1]) + "." + key
		}
		for _, p := range hygSensitiveSettings {
			if key == p || strings.HasPrefix(key, p+".") {
				add(m[0], fmt.Sprintf("updates editor setting %s", key))
				break
			}
		}
	}
	return out
}

// -- agent_config_write

var (
	hygAgentHighPathRe    = regexp.MustCompile(`(?:settings|mcp)\.json['"\x60]`)
	hygAgentHighContentRe = regexp.MustCompile(`bypassPermissions|allowedTools|dangerously`)
)

func hygAgentConfigWrite(c *FileCtx) []Finding {
	if !hygIsCode(c.Rel) {
		return nil
	}
	var out []Finding
	seen := map[string]bool{}
	for _, m := range hygAgentDirRe.FindAllSubmatchIndex(c.Data, -1) {
		win := hygWindow(c.Data, m[0], 300, 300)
		if !hygWriteVerbRe.Match(win) {
			continue
		}
		dir := string(c.Data[m[2]:m[3]])
		k := fmt.Sprintf("%d:%s", c.Line(m[0]), dir)
		if seen[k] {
			continue
		}
		seen[k] = true

		sev := Medium
		detail := fmt.Sprintf("writes under ~/%s; AI agents trust these files, so an extension writing them can steer another tool", dir)
		switch {
		case hygAgentHighPathRe.Match(win):
			sev = High
			detail += " (writes a settings.json/mcp.json)"
		case hygAgentHighContentRe.Match(win):
			sev = High
			detail += " (content touches permissions or tool allow-lists)"
		}
		out = append(out, Finding{
			Rule:     "agent_config_write",
			Severity: sev,
			File:     c.Rel,
			Line:     c.Line(m[0]),
			Snippet:  c.Snippet(m[0], 160),
			Detail:   detail,
		})
	}
	return out
}
