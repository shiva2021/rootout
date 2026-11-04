package app

import (
	"strings"
	"testing"
	"time"
)

func findLine(lines []FactLine, label string) (FactLine, bool) {
	for _, l := range lines {
		if l.Label == label {
			return l, true
		}
	}
	return FactLine{}, false
}

func TestSummary(t *testing.T) {
	signed := Facts{
		App:        App{Name: "Foo", Cask: "foo"},
		Signature:  Signature{Signed: true, TeamID: "TEAM", Authority: []string{"Developer ID Application: X (TEAM)", "Apple Root CA"}},
		Gatekeeper: Gatekeeper{Accepted: true, Source: "Notarized Developer ID"},
		Origin:     Origin{WhereFroms: []string{"https://x.example/a.dmg"}, DateAdded: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)},
		Plist:      PlistFacts{UIElement: true, UsageKeys: []string{"NSCameraUsageDescription", "NSMicrophoneUsageDescription"}, PrivilegedHelpers: []string{"com.foo.helper"}},
	}
	lines := signed.Summary()
	want := map[string]struct {
		value string
		tone  Tone
	}{
		"Signature":   {"Developer ID Application: X (TEAM) · team TEAM", Plain},
		"Gatekeeper":  {"accepted · Notarized Developer ID", Plain},
		"Where from":  {"https://x.example/a.dmg", Plain},
		"Added":       {"2026-11-01", Plain},
		"Runs":        {"hidden from the Dock", Plain},
		"Permissions": {"Camera, Microphone", Plain},
		"Helper":      {"com.foo.helper", Warn},
	}
	for label, w := range want {
		l, ok := findLine(lines, label)
		if !ok || !strings.HasPrefix(l.Value, w.value[:4]) || l.Tone != w.tone {
			t.Errorf("%s = %+v (found %v), want %q %v", label, l, ok, w.value, w.tone)
		}
		if label != "Added" && l.Value != w.value {
			t.Errorf("%s = %q, want %q", label, l.Value, w.value)
		}
	}
	if _, ok := findLine(lines, "Could not run"); ok {
		t.Error("nothing unavailable, no Could not run line")
	}
	if signed.Source() != "brew cask foo" {
		t.Errorf("Source = %q", signed.Source())
	}

	bare := Facts{Unavailable: []string{"codesign", "spctl", "mdls"}, AppStore: true}
	lines = bare.Summary()
	for _, label := range []string{"Signature", "Gatekeeper", "Origin", "Could not run"} {
		if l, ok := findLine(lines, label); !ok || l.Tone != Dim {
			t.Errorf("%s = %+v, want a dim not-available line", label, l)
		}
	}
	if bare.Source() != "app store" {
		t.Errorf("Source = %q", bare.Source())
	}

	unsigned := Facts{Gatekeeper: Gatekeeper{Source: "no usable signature"}}
	lines = unsigned.Summary()
	if l, _ := findLine(lines, "Signature"); l.Value != "unsigned" || l.Tone != Warn {
		t.Errorf("Signature = %+v", l)
	}
	if l, _ := findLine(lines, "Gatekeeper"); l.Value != "rejected · no usable signature" || l.Tone != Warn {
		t.Errorf("Gatekeeper = %+v", l)
	}
	if l, _ := findLine(lines, "Where from"); l.Value != "unknown" || l.Tone != Dim {
		t.Errorf("Where from = %+v", l)
	}
	if (Facts{App: App{Protected: true}}).Source() != "system" || (Facts{}).Source() != "" {
		t.Error("Source for system / unknown")
	}
}
