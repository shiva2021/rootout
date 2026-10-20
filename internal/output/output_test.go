package output

import (
	"bytes"
	"testing"
)

type app struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func TestPrint(t *testing.T) {
	two := Table{
		Columns: []string{"name", "size"},
		Cells:   [][]string{{"Slack", "1.2 GB"}, {"Xcode", "34.5 GB"}},
	}
	tests := []struct {
		name string
		json bool
		t    Table
		want string
	}{
		{
			name: "human two rows",
			t:    two,
			want: "NAME   SIZE\nSlack  1.2 GB\nXcode  34.5 GB\n",
		},
		{
			name: "human short row padded",
			t:    Table{Columns: []string{"a", "b"}, Cells: [][]string{{"x"}}},
			want: "A  B\nx  \n",
		},
		{
			name: "human empty",
			t:    Table{Columns: []string{"name", "size"}},
			want: "NAME  SIZE\n(none)\n",
		},
		{
			name: "json rows set marshals structs",
			json: true,
			t: Table{
				Columns: []string{"name", "size"},
				Cells:   [][]string{{"Slack", "1.2 GB"}},
				Rows:    []app{{Name: "Slack", Size: 1200000000}},
			},
			want: "[\n  {\n    \"name\": \"Slack\",\n    \"size\": 1200000000\n  }\n]\n",
		},
		{
			name: "json rows nil marshals cell maps with original column names",
			json: true,
			t:    Table{Columns: []string{"Name", "size"}, Cells: [][]string{{"Slack", "1.2 GB"}, {"Xcode"}}},
			want: "[\n  {\n    \"Name\": \"Slack\",\n    \"size\": \"1.2 GB\"\n  },\n  {\n    \"Name\": \"Xcode\",\n    \"size\": \"\"\n  }\n]\n",
		},
		{
			name: "json empty",
			json: true,
			t:    Table{Columns: []string{"name"}},
			want: "[]\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := New(&buf, tc.json)
			if w.JSON() != tc.json {
				t.Fatalf("JSON() = %v, want %v", w.JSON(), tc.json)
			}
			if err := w.Print(tc.t); err != nil {
				t.Fatalf("Print: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("Print output mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestPrintf(t *testing.T) {
	tests := []struct {
		name string
		json bool
		want string
	}{
		{name: "human writes", want: "removed 2 apps\n"},
		{name: "json suppressed", json: true, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			New(&buf, tc.json).Printf("removed %d apps\n", 2)
			if got := buf.String(); got != tc.want {
				t.Errorf("Printf output = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEmit(t *testing.T) {
	tests := []struct {
		name string
		json bool
		v    any
		want string
	}{
		{name: "human string", v: "ok", want: "ok\n"},
		{name: "json struct", json: true, v: app{Name: "Slack", Size: 1}, want: "{\n  \"name\": \"Slack\",\n  \"size\": 1\n}\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := New(&buf, tc.json).Emit(tc.v); err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("Emit output = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1000, "1 KB"},
		{1536, "2 KB"},
		{2_500_000, "3 MB"},
		{340_000_000, "340 MB"},
		{3_221_225_472, "3.2 GB"},
		{1_000_000_000_000, "1.0 TB"},
		{-1, "?"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := Bytes(tc.n); got != tc.want {
				t.Errorf("Bytes(%d) = %q, want %q", tc.n, got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{name: "shorter than max", s: "abc", max: 5, want: "abc"},
		{name: "equal to max", s: "abcde", max: 5, want: "abcde"},
		{name: "cut ascii", s: "abcdef", max: 4, want: "abc…"},
		{name: "cut multibyte", s: "日本語テキスト", max: 4, want: "日本語…"},
		{name: "cut accented", s: "ééééé", max: 3, want: "éé…"},
		{name: "max below two unchanged", s: "日本語", max: 1, want: "日本語"},
		{name: "max zero unchanged", s: "abc", max: 0, want: "abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Truncate(tc.s, tc.max); got != tc.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tc.s, tc.max, got, tc.want)
			}
		})
	}
}
