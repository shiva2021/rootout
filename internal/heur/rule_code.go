package heur

// Rule group A: code-level indicators. Every rule here is a pure function of
// one source file; helpers are prefixed "code" to stay out of the way of the
// other rule files in this package.

import (
	"bytes"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

func init() {
	for _, r := range []FileRule{
		codeRule("eval_dynamic", codeEvalDynamic),
		codeRule("child_process_shell", codeChildProcessShell),
		codeRule("obf_hex_identifiers", codeHexIdentifiers),
		codeRule("obf_escaped_strings", codeEscapedStrings),
		codeRule("obf_high_entropy_string", codeHighEntropyString),
		codeRule("obf_base64_blob", codeBase64Blob),
		codeRule("invisible_unicode", codeInvisibleUnicode),
		codeRule("remote_exec_chain", codeRemoteExecChain),
		codeRule("dynamic_require_net", codeDynamicRequireNet),
		codeRule("obf_long_line", codeLongLine),
	} {
		RegisterFile(r)
	}
}

// codeMaxFindings caps what one rule may report for one file.
const codeMaxFindings = 5

// codeExts are the source extensions the code rules look at. Manifests and
// data files (.json, .vsixmanifest, ...) are left to the meta rules.
var codeExts = map[string]bool{
	".js": true, ".cjs": true, ".mjs": true, ".jsx": true,
	".ts": true, ".tsx": true, ".mts": true, ".cts": true,
	".sh": true, ".py": true,
}

func codeIsSource(rel string) bool { return codeExts[strings.ToLower(path.Ext(rel))] }

// codeHit is one raw match before it becomes a Finding.
type codeHit struct {
	off    int
	sev    Severity
	detail string
	rule   string // overrides the rule name when set (invalid_utf8)
}

// codeRule wraps a hit-producing check with the source filter, per-line
// de-duplication and the per-file cap shared by every rule in this group.
func codeRule(name string, check func(c *FileCtx) []codeHit) FileRule {
	return FileRule{Name: name, Check: func(c *FileCtx) []Finding {
		if !codeIsSource(c.Rel) || len(c.Data) == 0 {
			return nil
		}
		return codeEmit(c, name, check(c))
	}}
}

// codeEmit turns hits into findings: sorted by offset, one per (rule, line),
// first five only, so a hostile file cannot flood the report.
func codeEmit(c *FileCtx, name string, hits []codeHit) []Finding {
	if len(hits) == 0 {
		return nil
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].off < hits[j].off })
	type key struct {
		rule string
		line int
	}
	seen := map[key]bool{}
	var out []Finding
	for _, h := range hits {
		rule := name
		if h.rule != "" {
			rule = h.rule
		}
		line := c.Line(h.off)
		if seen[key{rule, line}] {
			continue
		}
		seen[key{rule, line}] = true
		out = append(out, Finding{
			Rule:     rule,
			Severity: h.sev,
			File:     c.Rel,
			Line:     line,
			Snippet:  c.Snippet(h.off, 160),
			Detail:   h.detail,
		})
		if len(out) == codeMaxFindings {
			break
		}
	}
	return out
}

// codeEntropy is the Shannon entropy of b in bits per byte.
func codeEntropy(b []byte) float64 {
	if len(b) == 0 {
		return 0
	}
	var freq [256]int
	for _, x := range b {
		freq[x]++
	}
	n := float64(len(b))
	h := 0.0
	for _, f := range freq {
		if f > 0 {
			p := float64(f) / n
			h -= p * math.Log2(p)
		}
	}
	return h
}

// codeCall scans the argument list whose '(' is at data[open] and returns
// the text up to the matching ')' (or limit bytes) twice: raw, and as a
// skeleton with the bodies of '...' and "..." literals removed so that
// structural checks (+, .join() are not fooled by string contents. Template
// literals keep one "${}" marker per interpolation.
func codeCall(data []byte, open, limit int) (raw, skel []byte) {
	end := min(len(data), open+1+limit)
	depth := 0
	for i := open + 1; i < end; i++ {
		b := data[i]
		switch b {
		case '\'', '"', '`':
			skel = append(skel, b)
			j := i + 1
			for j < end && data[j] != b {
				switch {
				case data[j] == '\\':
					j++
				case b == '`' && data[j] == '$' && j+1 < end && data[j+1] == '{':
					skel = append(skel, '$', '{', '}')
					j = codeSkipBraces(data, j+1, end)
				}
				j++
			}
			skel = append(skel, b)
			i = j
		case '(', '[', '{':
			depth++
			skel = append(skel, b)
		case ')', ']', '}':
			if depth == 0 {
				return data[open+1 : i], skel
			}
			depth--
			skel = append(skel, b)
		default:
			skel = append(skel, b)
		}
	}
	return data[open+1 : end], skel
}

// codeSkipBraces returns the index of the '}' matching the '{' at data[at],
// or end-1 when it is unterminated.
func codeSkipBraces(data []byte, at, end int) int {
	depth := 0
	for k := at; k < end; k++ {
		switch data[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return end - 1
}

// codeFirstArg returns the skeleton text of the first argument.
func codeFirstArg(skel []byte) []byte {
	depth := 0
	for i, b := range skel {
		switch b {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				return skel[:i]
			}
		}
	}
	return skel
}

// codeDynamicReason says how the code string in args is assembled at
// runtime, or "" when it is static.
func codeDynamicReason(args, raw []byte) string {
	first := args
	switch {
	case bytes.Contains(first, []byte("${}")):
		return "template interpolation"
	case bytes.IndexByte(first, '+') >= 0:
		return "string concatenation"
	case bytes.Contains(first, []byte(".join(")):
		return "join()"
	case bytes.Contains(first, []byte("atob(")):
		return "atob() decode"
	case bytes.Contains(first, []byte("Buffer.from(")) && bytes.Contains(raw, []byte("base64")):
		return "base64 decode"
	case bytes.Contains(first, []byte("fromCharCode")):
		return "String.fromCharCode()"
	}
	return ""
}

// --- eval_dynamic -----------------------------------------------------------

var (
	reCodeEval        = regexp.MustCompile(`(^|[^\w$])eval\s*\(`)
	reCodePyExec      = regexp.MustCompile(`(^|[^\w.])exec\s*\(`)
	reCodeEvalLiteral = regexp.MustCompile(`^eval\s*\(\s*(?:'[^'\n]*'|"[^"\n]*")\s*\)`)
	reCodeNewFunction = regexp.MustCompile(`\bnew\s+Function\s*\(`)
	reCodeFunctionCal = regexp.MustCompile(`(^|[^\w$.])Function\s*\(`)
	reCodeTimerString = regexp.MustCompile(`\b(?:setTimeout|setInterval)\s*\(\s*['"` + "`" + `]`)
	reCodeVMRun       = regexp.MustCompile(`\brunIn(?:ThisContext|NewContext)\s*\(`)
	reCodeExecCall    = regexp.MustCompile(`\b(execSync|exec|spawn|spawnSync|execFile|execFileSync)\s*\(`)
)

func codeEvalDynamic(c *FileCtx) []codeHit {
	d := c.Data
	var hits []codeHit
	evalAt := func(off int) {
		if reCodeEvalLiteral.Match(d[off:]) {
			hits = append(hits, codeHit{off: off, sev: Medium, detail: "eval() of a string literal: code hidden from linters and search"})
			return
		}
		hits = append(hits, codeHit{off: off, sev: High, detail: "eval() of a computed value: runtime code execution that static review cannot see"})
	}
	for _, m := range reCodeEval.FindAllSubmatchIndex(d, -1) {
		evalAt(m[3])
	}
	if strings.EqualFold(path.Ext(c.Rel), ".py") {
		for _, m := range reCodePyExec.FindAllSubmatchIndex(d, -1) {
			hits = append(hits, codeHit{off: m[3], sev: High, detail: "exec() of a computed value: runtime code execution that static review cannot see"})
		}
	}
	for _, m := range reCodeNewFunction.FindAllIndex(d, -1) {
		hits = append(hits, codeHit{off: m[0], sev: High, detail: "new Function() compiles a string into code at runtime"})
	}
	for _, m := range reCodeFunctionCal.FindAllSubmatchIndex(d, -1) {
		off := m[3]
		if raw, skel := codeCall(d, m[1]-1, 600); codeDynamicReason(skel, raw) != "" { // body is the last argument
			hits = append(hits, codeHit{off: off, sev: High, detail: "Function() called with a string assembled at runtime"})
		}
	}
	for _, m := range reCodeTimerString.FindAllIndex(d, -1) {
		hits = append(hits, codeHit{off: m[0], sev: High, detail: "setTimeout/setInterval with a string argument is an implicit eval()"})
	}
	for _, m := range reCodeVMRun.FindAllIndex(d, -1) {
		hits = append(hits, codeHit{off: m[0], sev: High, detail: "vm.runIn*Context() executes a string as code"})
	}
	if bytes.Contains(d, []byte("child_process")) {
		for _, m := range reCodeExecCall.FindAllSubmatchIndex(d, -1) {
			raw, skel := codeCall(d, m[1]-1, 600)
			if why := codeDynamicReason(codeFirstArg(skel), raw); why != "" {
				fn := string(d[m[2]:m[3]])
				hits = append(hits, codeHit{off: m[0], sev: High, detail: fmt.Sprintf("child_process %s() with a command built at runtime (%s): the real command is not visible in the source", fn, why)})
			}
		}
	}
	return hits
}

// --- child_process_shell ----------------------------------------------------

var (
	reCodeCPImport = regexp.MustCompile(`(?:require\s*\(\s*|from\s+|import\s*\(\s*)['"](?:node:)?child_process['"]`)
	reCodeShellOpt = regexp.MustCompile(`shell\s*:\s*true|/bin/(?:ba|z)?sh|cmd\.exe|powershell|pwsh|osascript`)
	reCodePyShell  = regexp.MustCompile(`subprocess\.\w+\s*\([^)]*shell\s*=\s*True|os\.(?:system|popen)\s*\(`)
	reCodeShShell  = regexp.MustCompile(`\b(?:osascript|powershell|pwsh)\b|\b(?:ba|z)?sh\s+-c\b`)
)

func codeChildProcessShell(c *FileCtx) []codeHit {
	d := c.Data
	var hits []codeHit
	switch strings.ToLower(path.Ext(c.Rel)) {
	case ".py":
		for _, m := range reCodePyShell.FindAllIndex(d, -1) {
			hits = append(hits, codeHit{off: m[0], sev: Medium, detail: "runs a command through the system shell (shell=True / os.system): shell injection and hidden commands become possible"})
		}
		return hits
	case ".sh":
		for _, m := range reCodeShShell.FindAllIndex(d, -1) {
			hits = append(hits, codeHit{off: m[0], sev: Medium, detail: "invokes a scripting host (osascript/powershell/sh -c) from a bundled shell script"})
		}
		return hits
	}
	for _, m := range reCodeCPImport.FindAllIndex(d, -1) {
		hits = append(hits, codeHit{off: m[0], sev: Medium, detail: "imports child_process: the extension can run arbitrary programs on this machine"})
	}
	for _, m := range reCodeExecCall.FindAllSubmatchIndex(d, -1) {
		raw, _ := codeCall(d, m[1]-1, 600)
		if s := reCodeShellOpt.Find(raw); s != nil {
			hits = append(hits, codeHit{off: m[0], sev: Medium, detail: fmt.Sprintf("%s() goes through a shell or scripting host (%s): commands are interpreted, not just executed", d[m[2]:m[3]], s)})
		}
	}
	return hits
}

// --- obf_hex_identifiers ----------------------------------------------------

var reCodeHexIdent = regexp.MustCompile(`\b_0x[0-9a-f]{4,}\b`)

func codeHexIdentifiers(c *FileCtx) []codeHit {
	ms := reCodeHexIdent.FindAllIndex(c.Data, -1)
	if len(ms) == 0 {
		return nil
	}
	if len(ms) >= 10 {
		return []codeHit{{off: ms[0][0], sev: Medium, detail: fmt.Sprintf("%d _0x… identifiers: output of javascript-obfuscator, which renames everything to hide intent", len(ms))}}
	}
	perLine := map[int]int{}
	firstOff := map[int]int{}
	for _, m := range ms {
		l := c.Line(m[0])
		if perLine[l] == 0 {
			firstOff[l] = m[0]
		}
		perLine[l]++
	}
	var hits []codeHit
	for l, n := range perLine {
		if n >= 5 {
			hits = append(hits, codeHit{off: firstOff[l], sev: Medium, detail: fmt.Sprintf("%d _0x… identifiers on one line: output of javascript-obfuscator, which renames everything to hide intent", n)})
		}
	}
	return hits
}

// --- obf_escaped_strings ----------------------------------------------------

var (
	reCodeEscape    = regexp.MustCompile(`\\(?:x[0-9a-fA-F]{2}|u[0-9a-fA-F]{4}|u\{[0-9a-fA-F]{1,6}\})`)
	reCodeEscapeRun = regexp.MustCompile(`(?:\\(?:x[0-9a-fA-F]{2}|u[0-9a-fA-F]{4})){8,}`)
)

func codeEscapedStrings(c *FileCtx) []codeHit {
	var hits []codeHit
	for _, m := range reCodeEscapeRun.FindAllIndex(c.Data, -1) {
		n := len(reCodeEscape.FindAllIndex(c.Data[m[0]:m[1]], -1))
		hits = append(hits, codeHit{off: m[0], sev: Medium, detail: fmt.Sprintf("run of %d consecutive \\x/\\u escapes: text encoded so it cannot be read or grepped", n)})
	}
	if len(hits) > 0 {
		return hits
	}
	ms := reCodeEscape.FindAllIndex(c.Data, -1)
	if len(ms) >= 20 {
		hits = append(hits, codeHit{off: ms[0][0], sev: Medium, detail: fmt.Sprintf("%d \\x/\\u escapes in one file: strings encoded so they cannot be read or grepped", len(ms))})
	}
	return hits
}

// --- obf_high_entropy_string ------------------------------------------------

var (
	reCodeLongString = regexp.MustCompile(`"[^"\n]{200,}"|'[^'\n]{200,}'|` + "`[^`]{200,}`")
	reCodeHexTable   = regexp.MustCompile(`^[\s#0-9a-fA-F,;:]+$`)
)

func codeHighEntropyString(c *FileCtx) []codeHit {
	sev := Medium
	if !c.Minified() && !c.InNodeModules {
		sev = High
	}
	var hits []codeHit
	for _, m := range reCodeLongString.FindAllIndex(c.Data, -1) {
		s := c.Data[m[0]+1 : m[1]-1]
		if bytes.Contains(s, []byte("data:image")) || bytes.Contains(s, []byte("data:font")) ||
			bytes.Contains(s, []byte("sourceMappingURL")) || reCodeHexTable.Match(s) {
			continue
		}
		if h := codeEntropy(s); h >= 5.0 {
			hits = append(hits, codeHit{off: m[0], sev: sev, detail: fmt.Sprintf("string of %d chars with entropy %.2f bits/char: looks encrypted or packed, not text or code", len(s), h)})
		}
	}
	return hits
}

// --- obf_base64_blob --------------------------------------------------------

var (
	reCodeB64     = regexp.MustCompile(`[A-Za-z0-9+/=]{400,}`)
	reCodeB64Sink = regexp.MustCompile(`atob\s*\(|Buffer\.from\s*\(|\beval\s*\(|Function\s*\(|\bexec\w*\s*\(`)
)

func codeBase64Blob(c *FileCtx) []codeHit {
	d := c.Data
	var hits []codeHit
	for _, m := range reCodeB64.FindAllIndex(d, -1) {
		before := d[max(0, m[0]-100):m[0]]
		if bytes.Contains(before, []byte("sourceMappingURL")) {
			continue
		}
		after := d[m[1]:min(len(d), m[1]+200)]
		if reCodeB64Sink.Match(after) || reCodeB64Sink.Match(before) {
			hits = append(hits, codeHit{off: m[0], sev: Medium, detail: fmt.Sprintf("%d-char base64 blob decoded or executed nearby: a payload hidden from review", m[1]-m[0])})
			continue
		}
		hits = append(hits, codeHit{off: m[0], sev: Low, detail: fmt.Sprintf("%d-char base64 blob embedded in code", m[1]-m[0])})
	}
	return hits
}

// --- invisible_unicode ------------------------------------------------------

const (
	codeBlockVS     = "variation selectors U+FE00-FE0F"
	codeBlockVSSup  = "variation selectors supplement U+E0100-E01EF"
	codeBlockFormat = "zero-width/format characters U+200B-200F, U+2060-2064, U+FEFF"
	codeBlockPUA    = "private use area"
)

// codeInvisibleBlock names the block an invisible code point belongs to.
func codeInvisibleBlock(r rune) string {
	switch {
	case r >= 0xFE00 && r <= 0xFE0F:
		return codeBlockVS
	case r >= 0xE0100 && r <= 0xE01EF:
		return codeBlockVSSup
	case r >= 0x200B && r <= 0x200F, r >= 0x2060 && r <= 0x2064, r == 0xFEFF:
		return codeBlockFormat
	case r >= 0xE000 && r <= 0xF8FF, r >= 0xF0000 && r <= 0xFFFFD, r >= 0x100000 && r <= 0x10FFFD:
		return codeBlockPUA
	}
	return ""
}

func codeIdentByte(r rune) bool {
	return r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// codeIdentNext: the byte after the character is plain code (ASCII, not a quote).
func codeIdentNext(b byte) bool {
	return b < utf8.RuneSelf && b != '"' && b != '\'' && b != '`' && b != '\\'
}

// Thresholds for invisible_unicode: a payload run vs. a suspicious density.
const (
	codeInvisibleRun       = 8
	codeInvisibleScattered = 64
)

func codeInvisibleUnicode(c *FileCtx) []codeHit {
	d := c.Data
	ascii := true
	for _, b := range d {
		if b >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return nil
	}
	type blk struct{ count, first, run, maxRun int }
	blocks := map[string]*blk{}
	invalid := blk{first: -1}
	prev := rune(-1) // -1: start of file
	prevHit := false
	var cur *blk // block of the run in progress
	spoof := -1  // first invisible character inside an identifier
	for off := 0; off < len(d); {
		r, size := utf8.DecodeRune(d[off:])
		if r == utf8.RuneError && size == 1 {
			if invalid.first < 0 {
				invalid.first = off
			}
			invalid.count++
			off++
			prev, prevHit = r, false
			continue
		}
		name := codeInvisibleBlock(r)
		hit := false
		switch {
		case name == "":
		case r == 0xFEFF && off == 0: // byte-order mark
		case prevHit:
			hit = true // chained: part of a run
		case prev == -1 || prev < utf8.RuneSelf:
			// After ASCII the character has nothing legitimate to modify,
			// except a keycap base ('1' U+FE0F U+20E3).
			next, _ := utf8.DecodeRune(d[off+size:])
			keycap := name == codeBlockVS && strings.ContainsRune("0123456789#*", prev) && next == 0x20E3
			hit = !keycap
		default:
			// Follows a non-ASCII base (emoji + VS16, CJK + IVS, ZWJ in an
			// emoji sequence, bidi marks in RTL text): legitimate.
		}
		if hit && spoof < 0 && codeIdentByte(prev) && off+size < len(d) && codeIdentNext(d[off+size]) {
			spoof = off
		}
		if hit {
			b := blocks[name]
			if b == nil {
				b = &blk{first: off}
				blocks[name] = b
			}
			b.count++
			if cur != b {
				if cur != nil {
					cur.run = 0
				}
				cur = b
			}
			b.run++
			b.maxRun = max(b.maxRun, b.run)
		} else if cur != nil {
			cur.run, cur = 0, nil
		}
		prev, prevHit = r, hit
		off += size
	}
	var hits []codeHit
	for name, b := range blocks {
		// A hidden payload encodes bytes as long unbroken runs of invisible
		// characters. Scattered single ones appear legitimately in Unicode
		// data tables, HTML entity maps and charset tables.
		switch {
		case b.maxRun >= codeInvisibleRun:
			hits = append(hits, codeHit{off: b.first, sev: Critical, detail: fmt.Sprintf("run of %d invisible code points (%s; %d in file): GlassWorm-style hidden payload, invisible in editors and diffs", b.maxRun, name, b.count)})
		case b.count >= codeInvisibleScattered:
			hits = append(hits, codeHit{off: b.first, sev: Medium, detail: fmt.Sprintf("%d scattered invisible code points (%s): unusual density for source code", b.count, name)})
		default:
			hits = append(hits, codeHit{off: b.first, sev: Info, detail: fmt.Sprintf("%d isolated invisible code points (%s), typical of character tables", b.count, name)})
		}
	}
	if spoof >= 0 {
		hits = append(hits, codeHit{off: spoof, sev: High, rule: "invisible_identifier", detail: "zero-width or invisible character inside an identifier: two names that look identical can differ (Trojan Source-style spoofing)"})
	}
	if invalid.count > 0 {
		hits = append(hits, codeHit{off: invalid.first, sev: Info, rule: "invalid_utf8", detail: fmt.Sprintf("%d invalid UTF-8 bytes: not valid source text", invalid.count)})
	}
	return hits
}

// --- remote_exec_chain ------------------------------------------------------

var (
	reCodeDownload  = regexp.MustCompile(`https?\.get\s*\(|\bfetch\s*\(|\baxios\b|\bcurl\s|\bwget\s|Invoke-WebRequest|\burllib\b`)
	reCodeChmodX    = regexp.MustCompile(`\bchmod\s+(?:-R\s+)?(?:[ugoa]*\+[rw]*x|0?[1357][0-7]{2})\b|chmodSync\s*\([^)\n]{0,200}?,\s*(?:0o?[1357][0-7]{2}|['"]0?[1357][0-7]{2}['"]|493|511|448)\b`)
	reCodePSEnc     = regexp.MustCompile(`(?i)powershell[^\n]{0,80}?\s-enc\b|-EncodedCommand`)
	reCodePipeShell = regexp.MustCompile(`\|\s*(?:sudo\s+)?(?:ba|z)?sh\b`)
	reCodeWrite     = regexp.MustCompile(`\bwriteFile(?:Sync)?\s*\(|\bcreateWriteStream\s*\(|\bopen\s*\([^)\n]*['"][wa]b?['"]`)
	reCodeExecSite  = regexp.MustCompile(`\b(?:exec|execSync|spawn|spawnSync|execFile|execFileSync)\s*\(|subprocess\.\w+\s*\(|os\.system\s*\(`)
)

// codeChainWindow is how far apart (bytes) a download and an exec/chmod may
// be to count as one chain. Minified bundles put unrelated code in one file,
// so "same file" is meaningless; "same few lines" is not.
const codeChainWindow = 1500

// reCodeCLIDownload: only a command-line downloader can be piped into sh.
var reCodeCLIDownload = regexp.MustCompile(`\b(?:curl|wget)\s|Invoke-WebRequest|\biwr\s`)

var reCodeLiteralHTTPS = regexp.MustCompile(`(?:curl|wget)[^\n|]{0,80}?['"]?https://[A-Za-z0-9.-]+\.[a-z]{2,}/[^\s'"|]*['"]?\s*\|`)

// codeNear reports whether re matches within window bytes before off.
func codeNear(d []byte, off, window int, re *regexp.Regexp) bool {
	return re.Match(d[max(0, off-window):off])
}

func codeRemoteExecChain(c *FileCtx) []codeHit {
	d := c.Data
	if !reCodeDownload.Match(d) {
		return nil
	}
	var hits []codeHit
	for _, m := range reCodeChmodX.FindAllIndex(d, -1) {
		if codeNear(d, m[0], codeChainWindow, reCodeDownload) {
			hits = append(hits, codeHit{off: m[0], sev: Critical, detail: "downloads content and marks a file executable: the classic fetch -> chmod +x -> run second-stage loader"})
		}
	}
	for _, m := range reCodePSEnc.FindAllIndex(d, -1) {
		if codeNear(d, m[0], codeChainWindow, reCodeDownload) {
			hits = append(hits, codeHit{off: m[0], sev: Critical, detail: "downloads content and runs a base64-encoded PowerShell command"})
		}
	}
	for _, m := range reCodePipeShell.FindAllIndex(d, -1) {
		// The download tool must be on the same line, just before the pipe.
		line := d[max(bytes.LastIndexByte(d[:m[0]], '\n')+1, m[0]-300):m[1]]
		if !reCodeCLIDownload.Match(line) {
			continue
		}
		if reCodeLiteralHTTPS.Match(line) {
			hits = append(hits, codeHit{off: m[0], sev: Medium, detail: "pipes a fixed HTTPS install script into a shell (common for official installers, but runs whatever that URL serves)"})
		} else {
			hits = append(hits, codeHit{off: m[0], sev: Critical, detail: "pipes downloaded content from a non-fixed or plain-HTTP source straight into a shell"})
		}
	}
	if !bytes.Contains(d, []byte("child_process")) && !bytes.Contains(d, []byte("subprocess")) && !bytes.Contains(d, []byte("os.system")) {
		return hits
	}
	writes := reCodeWrite.FindAllIndex(d, -1)
	for _, e := range reCodeExecSite.FindAllIndex(d, -1) {
		if !codeNear(d, e[0], codeChainWindow, reCodeDownload) {
			continue
		}
		for _, w := range writes {
			if w[0] < e[0] && e[0]-w[0] <= codeChainWindow {
				hits = append(hits, codeHit{off: e[0], sev: Critical, detail: "downloads content, writes a file and executes it within a few lines: remote second-stage loader"})
				break
			}
		}
	}
	return hits
}

// --- dynamic_require_net ----------------------------------------------------

var (
	reCodeDynRequire = regexp.MustCompile(`(^|[^\w$])require\s*\(\s*(?:[A-Za-z_$][\w$]*\s*[)+.\[]|` + "`[^`\n]*\\$\\{)")
	reCodeNetRequire = regexp.MustCompile(`(?:require|import)\s*\(\s*['"](?:node:)?(https?|net|dns|tls|dgram)['"]\s*\)`)
	reCodeFunction   = regexp.MustCompile(`\bfunction\b|=>`)
)

func codeDynamicRequireNet(c *FileCtx) []codeHit {
	d := c.Data
	var hits []codeHit
	for _, m := range reCodeDynRequire.FindAllSubmatchIndex(d, -1) {
		hits = append(hits, codeHit{off: m[3], sev: Low, detail: "require() with a computed module name: the loaded code cannot be identified statically"})
	}
	if c.Minified() {
		return hits
	}
	first := reCodeFunction.FindIndex(d)
	if first == nil {
		return hits
	}
	top := map[string]bool{}
	type lazy struct {
		off int
		mod string
	}
	var lazies []lazy
	for _, m := range reCodeNetRequire.FindAllSubmatchIndex(d, -1) {
		mod := string(d[m[2]:m[3]])
		ls := bytes.LastIndexByte(d[:m[0]], '\n') + 1
		indented := d[ls] == ' ' || d[ls] == '\t'
		if !indented || m[0] < first[0] {
			top[mod] = true
			continue
		}
		lazies = append(lazies, lazy{m[0], mod})
	}
	for _, l := range lazies {
		if !top[l.mod] {
			hits = append(hits, codeHit{off: l.off, sev: Low, detail: fmt.Sprintf("require('%s') inside a function with no top-level import: network capability loaded lazily, easy to miss on a skim", l.mod)})
		}
	}
	return hits
}

// --- obf_long_line ----------------------------------------------------------

func codeLongLine(c *FileCtx) []codeHit {
	if c.Minified() {
		return nil
	}
	d := c.Data
	var hits []codeHit
	for start := 0; start < len(d); {
		end := bytes.IndexByte(d[start:], '\n')
		if end < 0 {
			end = len(d)
		} else {
			end += start
		}
		if line := d[start:end]; len(line) > 5000 && !bytes.Contains(line, []byte("sourceMappingURL")) {
			hits = append(hits, codeHit{off: start, sev: Low, detail: fmt.Sprintf("%d-char line in an otherwise normal file: a packed payload hiding in plain sight", len(line))})
		}
		start = end + 1
	}
	return hits
}
