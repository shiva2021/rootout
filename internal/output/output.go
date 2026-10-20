// Package output renders subcommand results either as JSON or as a
// human-readable table so every command looks the same under --json.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode/utf8"
)

// Writer renders either JSON or human text to an io.Writer.
type Writer struct {
	out  io.Writer
	json bool
}

// New returns a Writer that emits JSON when asJSON is true.
func New(out io.Writer, asJSON bool) *Writer {
	return &Writer{out: out, json: asJSON}
}

// JSON reports whether the writer is in JSON mode.
func (w *Writer) JSON() bool { return w.json }

// Table is a human-readable table. In JSON mode the same data is emitted as
// JSON: Rows if provided (so JSON carries full structs, not formatted cells),
// otherwise Cells as a list of maps keyed by column name.
type Table struct {
	Columns []string   // header cells
	Cells   [][]string // formatted cells, one row per entry
	Rows    any        // value to marshal in JSON mode; nil means marshal Cells
}

// Print writes the table. Human mode uses tabwriter with two-space padding,
// an upper-cased header and no borders; an empty table prints "(none)" under
// the header. JSON mode writes indented JSON with a trailing newline.
func (w *Writer) Print(t Table) error {
	if w.json {
		if t.Rows != nil {
			return w.writeJSON(t.Rows)
		}
		return w.writeJSON(t.cellMaps())
	}

	header := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = strings.ToUpper(c)
	}
	lines := []string{strings.Join(header, "\t")}
	if len(t.Cells) == 0 {
		lines = append(lines, "(none)")
	}
	for _, row := range t.Cells {
		lines = append(lines, strings.Join(padRow(row, len(t.Columns)), "\t"))
	}

	tw := tabwriter.NewWriter(w.out, 0, 0, 2, ' ', 0)
	for _, line := range lines {
		if _, err := fmt.Fprintln(tw, line); err != nil {
			return fmt.Errorf("output: write table: %w", err)
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("output: write table: %w", err)
	}
	return nil
}

// Printf writes human text only; it is a no-op in JSON mode so stdout stays
// parseable.
func (w *Writer) Printf(format string, args ...any) {
	if w.json {
		return
	}
	_, _ = fmt.Fprintf(w.out, format, args...) // human text; errors are not actionable here
}

// Emit writes an arbitrary value: indented JSON in JSON mode, fmt.Fprintln
// otherwise.
func (w *Writer) Emit(v any) error {
	if w.json {
		return w.writeJSON(v)
	}
	if _, err := fmt.Fprintln(w.out, v); err != nil {
		return fmt.Errorf("output: write: %w", err)
	}
	return nil
}

func (w *Writer) writeJSON(v any) error {
	enc := json.NewEncoder(w.out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("output: encode json: %w", err)
	}
	return nil
}

// cellMaps converts Cells into maps keyed by the original column names.
// It always returns a non-nil slice so an empty table marshals as [].
func (t Table) cellMaps() []map[string]string {
	rows := make([]map[string]string, 0, len(t.Cells))
	for _, row := range t.Cells {
		row = padRow(row, len(t.Columns))
		m := make(map[string]string, len(t.Columns))
		for i, c := range t.Columns {
			m[c] = row[i]
		}
		rows = append(rows, m)
	}
	return rows
}

// padRow returns row extended with "" up to n cells.
func padRow(row []string, n int) []string {
	if len(row) >= n {
		return row
	}
	padded := make([]string, n)
	copy(padded, row)
	return padded
}

// Bytes formats a byte count with SI units: one decimal for GB and above,
// none below ("1.2 GB", "340 MB", "12 KB", "0 B"). Negative counts return "?".
func Bytes(n int64) string {
	if n < 0 {
		return "?"
	}
	const k = 1000
	if n < k {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB", "PB", "EB"}
	unit := int64(k)
	i := 0
	for i < len(units)-1 && n >= unit*k {
		unit *= k
		i++
	}
	if i < 2 { // KB, MB: whole numbers, rounded half up
		return strconv.FormatInt((n+unit/2)/unit, 10) + " " + units[i]
	}
	tenth := unit / 10
	tenths := (n + tenth/2) / tenth
	return strconv.FormatInt(tenths/10, 10) + "." + strconv.FormatInt(tenths%10, 10) + " " + units[i]
}

// Truncate shortens s to max runes, replacing the tail with "…" when cut.
// max < 2 returns s unchanged.
func Truncate(s string, max int) string {
	if max < 2 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}
