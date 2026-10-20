package heur

// Rule group B: exfiltration and credential access. Every rule here is a
// pure function over one FileCtx; all patterns are compiled once at package
// init and findings are capped at exfilCap per rule per file.

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	exfilCap    = 5    // findings per rule per file
	exfilFlowKB = 1536 // bytes a file read may travel before it reaches a sink
	// exfilSep matches the glue between two path segments in a literal or a
	// path.join call: `/`, `\`, `', '`, `' + '/` and friends.
	exfilSep = `[\s'"\x60,+/\\]+`
)

var exfilCodeExts = map[string]bool{
	".js": true, ".cjs": true, ".mjs": true, ".jsx": true, ".ts": true, ".tsx": true, ".sh": true, ".py": true,
}

// exfilPat is one pattern with the label used in Detail and its severity.
type exfilPat struct {
	re    *regexp.Regexp
	label string
	sev   Severity
}

// exfilHostRe builds a case-insensitive, word-anchored host pattern.
func exfilHostRe(frag string) *regexp.Regexp {
	tail := ""
	if last := frag[len(frag)-1]; last >= 'a' && last <= 'z' || last >= '0' && last <= '9' {
		tail = `\b`
	}
	return regexp.MustCompile(`(?i)\b(?:` + frag + `)` + tail)
}

var exfilHosts = []exfilPat{
	{exfilHostRe(`discord(?:app)?\.com/api/webhooks`), "Discord webhook", High},
	// No trailing \b: real URLs continue straight into the token (/bot<TOKEN>).
	{regexp.MustCompile(`(?i)\bapi\.telegram\.org/bot`), "Telegram bot API", High},
	{exfilHostRe(`t\.me/`), "Telegram link", High},
	{exfilHostRe(`pastebin\.com`), "Pastebin", High},
	{exfilHostRe(`paste\.ee`), "paste.ee", High},
	{exfilHostRe(`hastebin`), "Hastebin", High},
	{exfilHostRe(`ngrok\.io|ngrok-free\.app`), "ngrok tunnel", High},
	{exfilHostRe(`trycloudflare\.com`), "Cloudflare quick tunnel", High},
	{exfilHostRe(`webhook\.site`), "webhook.site", High},
	{exfilHostRe(`requestbin`), "RequestBin", High},
	{exfilHostRe(`pipedream\.net`), "Pipedream", High},
	{exfilHostRe(`burpcollaborator`), "Burp Collaborator", High},
	{exfilHostRe(`oast\.[a-z]`), "OAST callback domain", High},
	{exfilHostRe(`interact\.sh`), "interact.sh", High},
	{exfilHostRe(`transfer\.sh`), "transfer.sh file host", Medium},
	{exfilHostRe(`0x0\.st`), "0x0.st file host", Medium},
	{exfilHostRe(`anonfiles`), "AnonFiles file host", Medium},
	{exfilHostRe(`gofile\.io`), "Gofile file host", Medium},
	{exfilHostRe(`mega\.nz`), "MEGA file host", Medium},
}

var (
	exfilIPURL      = regexp.MustCompile(`(?i)\b(https?|wss?)://(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\b`)
	exfilHTTPURL    = regexp.MustCompile(`(?i)\bhttp://([A-Za-z0-9.-]+)`)
	exfilHTTPClient = regexp.MustCompile(`\bfetch\s*\(|\baxios\b|\bhttps?\.request\b|\bXMLHttpRequest\b|\brequests\.(?:get|post)\b|\burllib\b`)
	exfilHTTPSkip   = []string{"localhost", "127.", "0.0.0.0", "www.w3.org", "schemas.", "json-schema.org", "purl.org", "xmlns.", "example."}
	exfilDottedIP   = regexp.MustCompile(`^\d{1,3}(?:\.\d{1,3}){3}$`)
)

// exfilAccessCtx: evidence that a credential path is actually resolved or read.
var exfilAccessCtx = regexp.MustCompile(`(?i)homedir|\bhome(?:dir|path)?\b|process\.env\.(?:HOME|USERPROFILE)|\$HOME|%USERPROFILE%|['"\x60]~/|expanduser|Path\.home\(|readFile|createReadStream|readdir|existsSync|\bopen\(`)

// exfilCredTokens are credential and wallet paths. Common words (atomic,
// ledger, keystore, Cookies, Exodus) need path or product context so an
// ordinary identifier does not fire.
var exfilCredTokens = []exfilPat{
	{regexp.MustCompile(`\.ssh\b`), ".ssh", High},
	{regexp.MustCompile(`\bid_rsa\b`), "id_rsa", High},
	{regexp.MustCompile(`\bid_ed25519\b`), "id_ed25519", High},
	{regexp.MustCompile(`\.aws` + exfilSep + `(?:credentials|config)\b`), ".aws/credentials", High},
	{regexp.MustCompile(`\.npmrc\b`), ".npmrc", High},
	{regexp.MustCompile(`\.yarnrc\b`), ".yarnrc", High},
	{regexp.MustCompile(`\.pypirc\b`), ".pypirc", High},
	{regexp.MustCompile(`\.netrc\b`), ".netrc", High},
	{regexp.MustCompile(`\.gitconfig\b`), ".gitconfig", High},
	{regexp.MustCompile(`\.git-credentials\b`), ".git-credentials", High},
	{regexp.MustCompile(`\.docker` + exfilSep + `config\.json`), ".docker/config.json", High},
	{regexp.MustCompile(`\.kube` + exfilSep + `config\b`), ".kube/config", High},
	{regexp.MustCompile(`\.gnupg\b`), ".gnupg", High},
	{regexp.MustCompile(`Library` + exfilSep + `Keychains`), "Library/Keychains", High},
	{regexp.MustCompile(`login\.keychain`), "login.keychain", High},
	{regexp.MustCompile(`(?:homedir\(\)|process\.env\.HOME|\$HOME|%USERPROFILE%|~)[^\n;]{0,40}?['"\x60/,]\s*['"\x60]?\.env\b`), "~/.env", High},
	{regexp.MustCompile(`(?:User Data|Chrom(?:e|ium)|Brave|Edge|Firefox|Profiles?|Default)[^\n]{0,60}?['"/\x60\\]Cookies['"\x60\\]`), "browser Cookies", High},
	{regexp.MustCompile(`['"/\x60\\](?:Login Data|Local State)['"\x60\\]`), "browser Login Data/Local State", High},
	{regexp.MustCompile(`\.config` + exfilSep + `solana`), ".config/solana", High},
	{regexp.MustCompile(`\.electrum\b`), ".electrum", High},
	{regexp.MustCompile(`(?i)exodus\.wallet|exodus[ _-]?wallet|['"/\x60\\]Exodus['"/\x60\\]`), "Exodus wallet", High},
	{regexp.MustCompile(`(?i)atomic[ _-]?wallet|['"/\x60\\]atomic['"/\x60\\][^\n]{0,30}?(?:Local Storage|leveldb|IndexedDB)`), "Atomic wallet", High},
	{regexp.MustCompile(`(?i)\bmetamask\b`), "MetaMask", High},
	{regexp.MustCompile(`nkbihfbeogaeaoehlefnkodbefgpgknn`), "MetaMask extension id", High},
	{regexp.MustCompile(`(?i)ledger[ _-]?live|@ledgerhq|ledger[ _-]?wallet`), "Ledger", High},
	{regexp.MustCompile(`wallet\.dat\b`), "wallet.dat", High},
	{regexp.MustCompile(`(?i)\.ethereum[/'"\x60,\s]+keystore|keystore/UTC--|['"/\x60\\]keystore['"/\x60\\]`), "keystore", High},
}

var exfilEnvDump = []exfilPat{
	{regexp.MustCompile(`JSON\.stringify\(\s*process\.env\s*\)`), "JSON.stringify(process.env)", Medium},
	{regexp.MustCompile(`Object\.(?:keys|entries|values)\(\s*process\.env\s*\)`), "Object.keys(process.env)", Medium},
	{regexp.MustCompile(`(?m)(?:\bfetch\s*\(|\baxios\b|\.post\s*\(|https?\.request\s*\(|\bbody\s*:|\bdata\s*:|\bjson\s*:)[^\n]{0,120}?(?:\.\.\.)?\bprocess\.env\s*(?:[,)\]}]|$)`), "process.env sent whole", Medium},
	{regexp.MustCompile(`dict\(\s*os\.environ\s*\)`), "dict(os.environ)", Medium},
	{regexp.MustCompile(`json\.dumps\(\s*os\.environ`), "json.dumps(os.environ)", Medium},
	{regexp.MustCompile(`\bprintenv\b`), "printenv", Medium},
}

var exfilEnvShell = regexp.MustCompile(`(?m)(?:^|[;&|(])\s*env\s*>`)

var exfilKeychain = []exfilPat{
	{regexp.MustCompile(`(?i)\bsecurity\s+(?:find-generic-password|find-internet-password|dump-keychain|export)\b`), "security keychain command", High},
	{regexp.MustCompile(`\bdefaults\s+read\b[^\n]*?(?i:password|token)`), "defaults read of a secret", High},
}

var exfilCapture = []exfilPat{
	{regexp.MustCompile(`\bpbpaste\b`), "pbpaste", Medium},
	{regexp.MustCompile(`\bclipboard\.readText\b`), "clipboard.readText", Medium},
	{regexp.MustCompile(`\bscreencapture\b`), "screencapture", Medium},
	{regexp.MustCompile(`\bxclip\s+-o\b`), "xclip -o", Medium},
	{regexp.MustCompile(`\bGet-Clipboard\b`), "Get-Clipboard", Medium},
	{regexp.MustCompile(`\bdesktopCapturer\b`), "desktopCapturer", Medium},
	{regexp.MustCompile(`\bgetDisplayMedia\b`), "getDisplayMedia", Medium},
}

var exfilC2 = []exfilPat{
	{regexp.MustCompile(`(?i)\bsolana\b|getSignaturesForAddress`), "Solana RPC", High},
	{regexp.MustCompile(`(?i)\bmemo\b[^\n]*\bprogram\b|\bprogram\b[^\n]*\bmemo\b`), "Solana memo program", High},
	{regexp.MustCompile(`bittorrent-dht|\bdht\.lookup\s*\(|magnet:\?xt=urn:btih`), "BitTorrent DHT", High},
	{regexp.MustCompile(`calendar\.app\.google`), "Google Calendar dead drop", High},
}

var (
	exfilGistURL = regexp.MustCompile(`api\.github\.com/gists`)
	exfilGistRaw = []byte("raw_url")
	exfilEval    = regexp.MustCompile(`\beval\s*\(|\bFunction\s*\(`)
)

var exfilTelemetry = []exfilPat{
	{regexp.MustCompile(`node-machine-id|\bmachineIdSync\b`), "node-machine-id fingerprint", Info},
	{regexp.MustCompile(`api\.segment\.io`), "Segment", Info},
	{regexp.MustCompile(`api\.amplitude\.com`), "Amplitude", Info},
	{regexp.MustCompile(`api\.mixpanel\.com`), "Mixpanel", Info},
	{regexp.MustCompile(`\bsentry\.io\b`), "Sentry", Info},
	{regexp.MustCompile(`app\.posthog\.com`), "PostHog", Info},
	{regexp.MustCompile(`google-analytics\.com/collect`), "Google Analytics", Info},
}

var (
	exfilRead    = regexp.MustCompile(`\b(?:readFile(?:Sync)?|fs\.promises\.readFile)\s*\(`)
	exfilReadPy  = regexp.MustCompile(`\bopen\s*\(`)
	exfilSink    = regexp.MustCompile(`\bfetch\s*\(|\baxios(?:\.(?:post|put|request))?\s*\(|\bhttps?\.request\s*\(|\bnew\s+FormData\s*\(|\bXMLHttpRequest\b|\brequests\.post\s*\(|\burllib\.request\b`)
	exfilHomeKey = regexp.MustCompile(`(?:homedir\(\)|process\.env\.HOME|\$HOME|expanduser|~)[^\n]{0,80}?\.(?:pem|key|p12|json)\b`)
)

func init() {
	RegisterFile(FileRule{Name: "suspicious_host", Check: exfilSuspiciousHost})
	RegisterFile(FileRule{Name: "raw_ip_url", Check: exfilRawIPURL})
	RegisterFile(FileRule{Name: "plain_http_beacon", Check: exfilPlainHTTPBeacon})
	RegisterFile(FileRule{Name: "credential_file_access", Check: exfilCredentialFileAccess})
	RegisterFile(FileRule{Name: "env_dump", Check: exfilEnvDumpRule})
	RegisterFile(FileRule{Name: "keychain_read", Check: exfilKeychainRead})
	RegisterFile(FileRule{Name: "clipboard_or_screen_capture", Check: exfilClipboardOrScreenCapture})
	RegisterFile(FileRule{Name: "c2_blockchain_or_dht", Check: exfilC2BlockchainOrDHT})
	RegisterFile(FileRule{Name: "telemetry_fingerprint", Check: exfilTelemetryFingerprint})
	RegisterFile(FileRule{Name: "exfil_post_of_file", Check: exfilPostOfFile})
}

// --- helpers ---------------------------------------------------------------

func exfilIsCode(rel string) bool { return exfilCodeExts[strings.ToLower(path.Ext(rel))] }

func exfilIsCodeOrJSON(rel string) bool {
	return exfilIsCode(rel) || strings.EqualFold(path.Ext(rel), ".json")
}

// exfilEmitter collects findings for one rule: one per line, at most exfilCap.
type exfilEmitter struct {
	c    *FileCtx
	rule string
	seen map[int]bool
	out  []Finding
}

func exfilNew(c *FileCtx, rule string) *exfilEmitter {
	return &exfilEmitter{c: c, rule: rule, seen: map[int]bool{}}
}

func (e *exfilEmitter) full() bool { return len(e.out) >= exfilCap }

// add records a finding at off unless the cap is reached or the line was
// already reported. The Rule may be overridden (cloud_metadata_endpoint).
func (e *exfilEmitter) add(rule string, sev Severity, off int, detail string) {
	if e.full() {
		return
	}
	line := e.c.Line(off)
	if e.seen[line] {
		return
	}
	e.seen[line] = true
	if rule == "" {
		rule = e.rule
	}
	e.out = append(e.out, Finding{
		Rule: rule, Severity: sev, File: e.c.Rel, Line: line,
		Snippet: e.c.Snippet(off, 160), Detail: detail,
	})
}

// scan applies a pattern table, formatting Detail with the pattern label.
func (e *exfilEmitter) scan(pats []exfilPat, format string, accept func(off, end int) bool) {
	for _, p := range pats {
		if e.full() {
			return
		}
		for _, m := range p.re.FindAllIndex(e.c.Data, exfilCap*8) {
			if accept != nil && !accept(m[0], m[1]) {
				continue
			}
			e.add("", p.sev, m[0], fmt.Sprintf(format, p.label))
		}
	}
}

// exfilLineStart returns the offset of the first byte of the line holding off.
func exfilLineStart(data []byte, off int) int {
	return bytes.LastIndexByte(data[:off], '\n') + 1
}

// exfilCommentLine reports whether the line holding off starts like a comment.
func exfilCommentLine(data []byte, off int) bool {
	rest := bytes.TrimLeft(data[exfilLineStart(data, off):], " \t")
	return bytes.HasPrefix(rest, []byte("//")) || bytes.HasPrefix(rest, []byte("*")) ||
		bytes.HasPrefix(rest, []byte("#")) || bytes.HasPrefix(rest, []byte("/*"))
}

// exfilQuoted reports whether a string delimiter appears on the line before
// end, i.e. the match [start,end) plausibly sits inside a string literal.
func exfilQuoted(data []byte, start, end int) bool {
	return bytes.ContainsAny(data[exfilLineStart(data, start):end], "'\"`")
}

// exfilIPClass labels a dotted IPv4: "invalid", "metadata", "loopback",
// "private", "linklocal" or "public".
func exfilIPClass(octets []string) string {
	var o [4]int
	for i, s := range octets {
		n, err := strconv.Atoi(s)
		if err != nil || n > 255 {
			return "invalid"
		}
		o[i] = n
	}
	switch {
	case o == [4]int{169, 254, 169, 254}:
		return "metadata"
	case o[0] == 127 || o == [4]int{0, 0, 0, 0}:
		return "loopback"
	case o[0] == 10, o[0] == 192 && o[1] == 168, o[0] == 172 && o[1] >= 16 && o[1] <= 31:
		return "private"
	case o[0] == 169 && o[1] == 254:
		return "linklocal"
	}
	return "public"
}

// --- rules -----------------------------------------------------------------

// suspicious_host: known exfiltration, tunnelling and dead-drop services.
func exfilSuspiciousHost(c *FileCtx) []Finding {
	if !exfilIsCodeOrJSON(c.Rel) {
		return nil
	}
	e := exfilNew(c, "suspicious_host")
	e.scan(exfilHosts, "URL for %s, a common exfiltration or C2 channel", nil)
	return e.out
}

// raw_ip_url: requests addressed to a bare IPv4 instead of a hostname.
func exfilRawIPURL(c *FileCtx) []Finding {
	if !exfilIsCode(c.Rel) {
		return nil
	}
	e := exfilNew(c, "raw_ip_url")
	for _, m := range exfilIPURL.FindAllSubmatchIndex(c.Data, exfilCap*8) {
		if m[1] < len(c.Data) && c.Data[m[1]] == '.' {
			continue // longer dotted sequence, not an IPv4
		}
		scheme := strings.ToLower(string(c.Data[m[2]:m[3]]))
		octets := make([]string, 4)
		for i := range octets {
			octets[i] = string(c.Data[m[4+2*i]:m[5+2*i]])
		}
		ip := strings.Join(octets, ".")
		class := exfilIPClass(octets)
		isWS := strings.HasPrefix(scheme, "ws")
		switch {
		case class == "metadata":
			e.add("cloud_metadata_endpoint", Info, m[0], "references the cloud instance metadata address "+ip+" (expected in cloud SDKs)")
		case class == "invalid", class == "loopback":
		case isWS:
			e.add("", Medium, m[0], scheme+":// connection to raw IP "+ip)
		case class != "public":
		case scheme == "http":
			e.add("", High, m[0], "plain http:// request to raw public IP "+ip)
		default:
			e.add("", Medium, m[0], "https:// request to raw public IP "+ip)
		}
	}
	return e.out
}

// plain_http_beacon: unencrypted HTTP targets in a file that makes requests.
func exfilPlainHTTPBeacon(c *FileCtx) []Finding {
	if !exfilIsCodeOrJSON(c.Rel) || !exfilHTTPClient.Match(c.Data) {
		return nil
	}
	e := exfilNew(c, "plain_http_beacon")
	for _, m := range exfilHTTPURL.FindAllSubmatchIndex(c.Data, exfilCap*8) {
		host := strings.ToLower(string(c.Data[m[2]:m[3]]))
		if !strings.Contains(host, ".") || exfilDottedIP.MatchString(host) || exfilCommentLine(c.Data, m[0]) {
			continue // placeholder host, raw IP (raw_ip_url) or doc/licence link
		}
		skip := false
		for _, p := range exfilHTTPSkip {
			if strings.HasPrefix(host, p) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		e.add("", Low, m[0], "plain http:// request target "+host+" (no TLS)")
	}
	return e.out
}

// credential_file_access: string literals naming credential or wallet paths.
func exfilCredentialFileAccess(c *FileCtx) []Finding {
	if !exfilIsCode(c.Rel) || strings.Contains("/"+strings.ToLower(c.Rel), "/test") {
		return nil
	}
	e := exfilNew(c, "credential_file_access")
	shell := strings.EqualFold(path.Ext(c.Rel), ".sh")
	e.scan(exfilCredTokens, "references credential path %s", func(off, end int) bool {
		if shell {
			return true
		}
		// A bare file name in a list (syntax-highlighting tables, file
		// watchers) is not access; require a home-dir or file-read context.
		return exfilQuoted(c.Data, off, end) && exfilAccessCtx.Match(c.Data[max(0, off-200):min(len(c.Data), end+120)])
	})
	return e.out
}

// env_dump: the whole environment serialised or sent, not a single variable.
func exfilEnvDumpRule(c *FileCtx) []Finding {
	if !exfilIsCode(c.Rel) {
		return nil
	}
	e := exfilNew(c, "env_dump")
	for _, p := range exfilEnvDump {
		for _, m := range p.re.FindAllIndex(c.Data, exfilCap*8) {
			// Passing the environment to a child process is routine; sending
			// it to the network is exfiltration (an intent rule, see D-017).
			win := c.Data[max(0, m[0]-300):min(len(c.Data), m[1]+300)]
			if exfilHTTPClient.Match(win) {
				e.add("env_exfil", High, m[0], "whole environment sent in a network request: "+p.label)
			} else {
				e.add("", p.sev, m[0], "whole environment serialised: "+p.label)
			}
		}
	}
	if strings.EqualFold(path.Ext(c.Rel), ".sh") {
		for _, m := range exfilEnvShell.FindAllIndex(c.Data, exfilCap) {
			e.add("", Medium, m[0], "whole environment serialised: env > file")
		}
	}
	return e.out
}

// keychain_read: macOS keychain and defaults reads of secrets.
func exfilKeychainRead(c *FileCtx) []Finding {
	if !exfilIsCode(c.Rel) {
		return nil
	}
	e := exfilNew(c, "keychain_read")
	e.scan(exfilKeychain, "reads macOS secrets via %s", nil)
	return e.out
}

// clipboard_or_screen_capture: clipboard and screen reads outside vendored
// UI libraries, which legitimately mention these APIs.
func exfilClipboardOrScreenCapture(c *FileCtx) []Finding {
	if !exfilIsCode(c.Rel) || c.InNodeModules || strings.Contains(c.Rel, "node_modules/") {
		return nil
	}
	e := exfilNew(c, "clipboard_or_screen_capture")
	e.scan(exfilCapture, "reads clipboard or screen via %s", func(off, _ int) bool {
		// vscode.env.clipboard is the editor API behind paste-a-URL prompts,
		// not a covert read.
		return !bytes.HasSuffix(c.Data[:off], []byte("env."))
	})
	return e.out
}

// c2_blockchain_or_dht: command channels that cannot be taken down.
func exfilC2BlockchainOrDHT(c *FileCtx) []Finding {
	if !exfilIsCode(c.Rel) {
		return nil
	}
	e := exfilNew(c, "c2_blockchain_or_dht")
	e.scan(exfilC2, "possible C2 channel: %s", nil)
	if bytes.Contains(c.Data, exfilGistRaw) && exfilEval.Match(c.Data) {
		for _, m := range exfilGistURL.FindAllIndex(c.Data, exfilCap) {
			e.add("", High, m[0], "possible C2 channel: GitHub gist dead drop (gists API + raw_url + eval)")
		}
	}
	return e.out
}

// telemetry_fingerprint: analytics endpoints and machine ids, for visibility.
func exfilTelemetryFingerprint(c *FileCtx) []Finding {
	if !exfilIsCode(c.Rel) {
		return nil
	}
	e := exfilNew(c, "telemetry_fingerprint")
	e.scan(exfilTelemetry, "phones home to %s", nil)
	return e.out
}

// exfil_post_of_file: a credential file read followed closely by a network
// send. Data flow is approximated by proximity: the read's surroundings must
// name a credential path (or a *.pem/*.key/*.p12/*.json under the home dir)
// and a sink must appear within exfilFlowKB bytes after it.
func exfilPostOfFile(c *FileCtx) []Finding {
	if !exfilIsCode(c.Rel) || !exfilSink.Match(c.Data) {
		return nil
	}
	reads := exfilRead.FindAllIndex(c.Data, -1)
	if strings.EqualFold(path.Ext(c.Rel), ".py") {
		reads = append(reads, exfilReadPy.FindAllIndex(c.Data, -1)...)
	}
	e := exfilNew(c, "exfil_post_of_file")
	for _, r := range reads {
		if e.full() {
			break
		}
		lo, hi := max(r[0]-256, 0), min(r[1]+256, len(c.Data))
		window := c.Data[lo:hi]
		label := exfilCredIn(window)
		if label == "" {
			continue
		}
		end := min(r[1]+exfilFlowKB, len(c.Data))
		s := exfilSink.FindIndex(c.Data[r[1]:end])
		if s == nil {
			continue
		}
		sink := strings.TrimRight(string(c.Data[r[1]+s[0]:r[1]+s[1]]), "( \t")
		e.add("", High, r[1]+s[0], "file read of "+label+" flows into "+sink)
	}
	return e.out
}

// exfilCredIn names the credential token found in window, or "".
func exfilCredIn(window []byte) string {
	for _, p := range exfilCredTokens {
		if p.re.Match(window) {
			return p.label
		}
	}
	if exfilHomeKey.Match(window) {
		return "a key or config file under the home directory"
	}
	return ""
}
