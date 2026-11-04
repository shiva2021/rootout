package app

import (
	"bufio"
	"bytes"
	"strings"
	"time"
)

// Signature is what `codesign -dv --verbose=4` reports about a bundle. An
// unsigned bundle has Signed false and nothing else; an ad-hoc signature
// has Signed and AdHoc with no TeamID.
type Signature struct {
	Signed     bool     `json:"signed"`
	AdHoc      bool     `json:"adhoc,omitempty"`
	Identifier string   `json:"identifier,omitempty"` // the signing identifier, usually the bundle id
	TeamID     string   `json:"team_id,omitempty"`
	Authority  []string `json:"authority,omitempty"` // certificate chain, leaf first; untrusted text
	Timestamp  string   `json:"timestamp,omitempty"` // as printed by codesign
}

// Gatekeeper is the verdict of `spctl --assess --type execute -vv`.
type Gatekeeper struct {
	Accepted bool   `json:"accepted"`
	Source   string `json:"source,omitempty"` // "Notarized Developer ID", "no usable signature", or the failure text
}

// Origin is the Spotlight metadata that records where a bundle came from
// and when it was last opened. Zero values mean Spotlight has no record.
type Origin struct {
	WhereFroms []string  `json:"where_froms,omitempty"` // download URLs; untrusted text
	DateAdded  time.Time `json:"date_added,omitempty"`
	LastUsed   time.Time `json:"last_used,omitempty"`
}

// parseCodesign reads codesign's key=value lines. "not signed at all" (exit
// 1) is an answer, not a failure: the result is an unsigned Signature.
func parseCodesign(out []byte) Signature {
	var s Signature
	if bytes.Contains(out, []byte("not signed at all")) {
		return s
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "Identifier":
			s.Identifier = value
			s.Signed = true
		case "TeamIdentifier":
			if value != "not set" {
				s.TeamID = value
			}
		case "Authority":
			s.Authority = append(s.Authority, value)
		case "Timestamp", "Signed Time":
			s.Timestamp = value
		case "Signature":
			if value == "adhoc" {
				s.AdHoc = true
			}
		case "CodeDirectory v":
			if strings.Contains(value, "adhoc") {
				s.AdHoc = true
			}
		}
	}
	return s
}

// parseSpctl reads spctl's verdict: "<path>: accepted" or "<path>: rejected"
// on the first line, then "source=…". Any other first line (for example
// "code has no resources but signature indicates they must be present") is
// a rejection whose text becomes the source.
func parseSpctl(out []byte) Gatekeeper {
	var g Gatekeeper
	sc := bufio.NewScanner(bytes.NewReader(out))
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			switch {
			case strings.HasSuffix(line, ": accepted"):
				g.Accepted = true
			case strings.HasSuffix(line, ": rejected"):
			default:
				if i := strings.LastIndex(line, ": "); i >= 0 {
					g.Source = line[i+2:]
				} else if line != "" {
					g.Source = line
				}
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "source="); ok {
			g.Source = value
		}
	}
	return g
}

// mdlsTime is the layout mdls prints dates in.
const mdlsTime = "2006-01-02 15:04:05 -0700"

// parseMdls reads `mdls -name … ` output: one "key = value" per line, where a
// value is "(null)", a date, or a parenthesised list spread over lines.
func parseMdls(out []byte) Origin {
	var o Origin
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if value == "(null)" {
			continue
		}
		switch key {
		case "kMDItemDateAdded":
			o.DateAdded = parseMdlsTime(value)
		case "kMDItemLastUsedDate":
			o.LastUsed = parseMdlsTime(value)
		case "kMDItemWhereFroms":
			if value != "(" {
				continue
			}
			for sc.Scan() {
				item := strings.TrimSpace(sc.Text())
				if item == ")" {
					break
				}
				item = strings.Trim(strings.TrimSuffix(item, ","), `"`)
				if item != "" {
					o.WhereFroms = append(o.WhereFroms, item)
				}
			}
		}
	}
	return o
}

func parseMdlsTime(s string) time.Time {
	t, err := time.Parse(mdlsTime, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
