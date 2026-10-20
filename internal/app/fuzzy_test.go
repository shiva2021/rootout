package app

import (
	"errors"
	"reflect"
	"testing"
)

var fixture = []App{
	{Name: "Visual Studio Code", BundleID: "com.microsoft.VSCode", Path: "/Applications/Visual Studio Code.app"},
	{Name: "Spotify", BundleID: "com.spotify.client", Path: "/Applications/Spotify.app"},
	{Name: "Safari", BundleID: "com.apple.Safari", Path: "/Applications/Safari.app", Protected: true},
	{Name: "1Password", BundleID: "com.1password.1password", Path: "/Applications/1Password.app"},
	{Name: "Google Chrome", BundleID: "com.google.Chrome", Path: "/Applications/Google Chrome.app"},
	{Name: "Ghostty", BundleID: "com.mitchellh.ghostty", Path: "/Applications/Ghostty.app"},
	{Name: "R", BundleID: "org.R-project.R", Path: "/Applications/R.app"},
	{Name: "Raycast", BundleID: "com.raycast.macos", Path: "/Applications/Raycast.app"},
}

func names(ms []Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.App.Name
	}
	return out
}

func TestFuzzy(t *testing.T) {
	tests := []struct {
		name    string
		q       string
		first   string // expected App.Name of the best match
		rank    int    // expected Rank of the best match
		idx     []int  // expected MatchedIndexes of the best match (nil = don't check)
		wantLen int    // expected number of matches (0 = don't check)
	}{
		{name: "exact name", q: "spotify", first: "Spotify", rank: 1},
		{name: "exact bundle id", q: "com.spotify.client", first: "Spotify", rank: 0},
		{name: "prefix", q: "Goo", first: "Google Chrome", rank: 2, idx: []int{0, 1, 2}, wantLen: 1},
		{name: "via bundle id", q: "vscode", first: "Visual Studio Code", rank: 3},
		{name: "prefix beats fuzzy", q: "ray", first: "Raycast", rank: 2},
		{name: "no hit", q: "zzzz", wantLen: 0, first: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Fuzzy(fixture, tc.q)
			if tc.first == "" {
				if len(got) != 0 {
					t.Fatalf("want no matches, got %v", names(got))
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("want %q first, got no matches", tc.first)
			}
			if got[0].App.Name != tc.first || got[0].Rank != tc.rank {
				t.Fatalf("first = %q rank %d, want %q rank %d (all: %v)",
					got[0].App.Name, got[0].Rank, tc.first, tc.rank, names(got))
			}
			if tc.idx != nil && !reflect.DeepEqual(got[0].MatchedIndexes, tc.idx) {
				t.Fatalf("MatchedIndexes = %v, want %v", got[0].MatchedIndexes, tc.idx)
			}
			if tc.wantLen != 0 && len(got) != tc.wantLen {
				t.Fatalf("len = %d, want %d (%v)", len(got), tc.wantLen, names(got))
			}
		})
	}
}

func TestFuzzyEmptyQuery(t *testing.T) {
	got := Fuzzy(fixture, "")
	if len(got) != len(fixture) {
		t.Fatalf("len = %d, want %d", len(got), len(fixture))
	}
	for i, m := range got {
		if m.App.Name != fixture[i].Name || m.Rank != 3 || m.Score != 0 || m.MatchedIndexes != nil {
			t.Fatalf("match %d = %+v, want %q rank 3 score 0 no highlights", i, m, fixture[i].Name)
		}
	}
}

func TestFuzzyRaycastAboveR(t *testing.T) {
	got := Fuzzy(fixture, "ray")
	pos := map[string]int{}
	for i, n := range names(got) {
		pos[n] = i
	}
	rc, ok := pos["Raycast"]
	if !ok {
		t.Fatalf("Raycast missing from %v", names(got))
	}
	if r, ok := pos["R"]; ok && r < rc {
		t.Fatalf("R ranked above Raycast: %v", names(got))
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		arg  string
		want string
	}{
		{"Spotify", "Spotify"},
		{"spotify", "Spotify"},
		{"com.google.Chrome", "Google Chrome"},
		{"/Applications/Ghostty.app", "Ghostty"},
		{"/Applications/Ghostty.app/", "Ghostty"},
		{"Ray", "Raycast"},
	}
	for _, tc := range tests {
		t.Run(tc.arg, func(t *testing.T) {
			got, err := Resolve(fixture, tc.arg)
			if err != nil {
				t.Fatalf("Resolve(%q) error: %v", tc.arg, err)
			}
			if got.Name != tc.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tc.arg, got.Name, tc.want)
			}
		})
	}
}

func TestResolveNotFound(t *testing.T) {
	_, err := Resolve(fixture, "zzzz")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolveAmbiguous(t *testing.T) {
	apps := append([]App{}, fixture...)
	apps = append(apps, App{Name: "Google Drive", BundleID: "com.google.drive", Path: "/Applications/Google Drive.app"})

	_, err := Resolve(apps, "Goo")
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want *AmbiguousError", err)
	}
	if amb.Arg != "Goo" {
		t.Fatalf("Arg = %q, want %q", amb.Arg, "Goo")
	}
	want := []string{"Google Chrome", "Google Drive"}
	if got := names(amb.Candidates); !reflect.DeepEqual(got, want) {
		t.Fatalf("Candidates = %v, want %v", got, want)
	}
	if amb.Error() == "" {
		t.Fatal("Error() is empty")
	}
}
