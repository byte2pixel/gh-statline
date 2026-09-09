package export

import (
	"encoding/csv"
	"fmt"
	"strings"
)

// CSV renders a Doc for a spreadsheet: a header row of column keys, then
// the rows. No title and no metadata, so the file opens as a table.
//
// Headers are the machine keys, not the display headings, so rewording a
// heading never renames a spreadsheet column.
//
// A view with two sheets writes both in order, separated by a blank line,
// each with its own header row.
func CSV(d Doc) (string, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)
	for i, s := range d.sheets(true) {
		if i > 0 {
			b.WriteString("\n")
		}
		cols := s.columns(true)
		head := make([]string, len(cols))
		for j, c := range cols {
			head[j] = c.Key
		}
		if err := w.Write(head); err != nil {
			return "", fmt.Errorf("writing csv header: %w", err)
		}
		for _, r := range s.Rows {
			cells := s.cells(r, true)
			rec := make([]string, len(cells))
			for j, v := range cells {
				rec[j] = v.plain()
			}
			if err := w.Write(rec); err != nil {
				return "", fmt.Errorf("writing csv row: %w", err)
			}
		}
		// Flush per sheet: the blank line goes straight to the builder
		// underneath the buffered writer.
		w.Flush()
		if err := w.Error(); err != nil {
			return "", fmt.Errorf("writing csv: %w", err)
		}
	}
	return b.String(), nil
}
