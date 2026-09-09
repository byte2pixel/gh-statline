package export

import (
	"strconv"
	"strings"
	"time"

	"github.com/byte2pixel/gh-statline/internal/text"
)

// A Doc is one view's data before formatting. Markdown, CSV and JSON all
// render from it, so the three cannot disagree about a number or a column.

// Scope is which formats carry a column or a sheet.
type Scope int

const (
	ScopeAll Scope = iota
	// ScopeMarkdown is phrased for a reader ("4m ago"). Its raw value
	// travels beside it as ScopeMachine.
	ScopeMarkdown
	// ScopeMachine is what Markdown words itself (the movers bullets) or
	// would truncate in a table cell (a sync failure).
	ScopeMachine
)

func (s Scope) inMarkdown() bool { return s != ScopeMachine }
func (s Scope) inMachine() bool  { return s != ScopeMarkdown }

// kind is what the renderers disagree about: Markdown wants "26.0h" and
// "–", CSV wants 93600 and an empty field, JSON wants 93600 and null.
type kind int

const (
	kindText kind = iota
	kindNum
	kindDur
	kindBool
	kindNone
)

// Cell is one typed value. The constructors are the only way to build one,
// so the "no data" sentinels collapse to kindNone here rather than in each
// renderer. Text is stored raw and sanitized on the way out: what needs
// escaping depends on the format.
type Cell struct {
	kind kind
	text string
	num  int64
	dur  time.Duration
	flag bool
}

// Text is an untrusted string: a login, a repo name, an API error.
func Text(s string) Cell { return Cell{kind: kindText, text: s} }

// Num is a count. Zero is a real count, not a sentinel.
func Num(n int) Cell { return Cell{kind: kindNum, num: int64(n)} }

// Dur is a median duration; zero is the no-data sentinel
// (metrics.Row.CycleTimeP50, TTFRP50).
func Dur(d time.Duration) Cell {
	if d == 0 {
		return None()
	}
	return Cell{kind: kindDur, dur: d}
}

// Size is a median PR size; -1 is the no-data sentinel
// (metrics.Row.SizeP50).
func Size(n int) Cell {
	if n < 0 {
		return None()
	}
	return Num(n)
}

func Bool(b bool) Cell { return Cell{kind: kindBool, flag: b} }

// Timestamp is a cache timestamp (unix seconds UTC, nil for never) as
// RFC 3339.
func Timestamp(ts *int64) Cell {
	if ts == nil {
		return None()
	}
	return Text(time.Unix(*ts, 0).UTC().Format(time.RFC3339))
}

// None is a dash in Markdown, an empty CSV field, null in JSON. Never 0 or
// -1, which a consumer would read as data.
func None() Cell { return Cell{kind: kindNone} }

// Column names one field twice. Key is the machine name CSV headers and
// JSON keys use; Header is what a reader sees. Rewording a Header must not
// rename a Key.
type Column struct {
	Key    string
	Header string
	// Numeric right-aligns the Markdown column. Declared, not sniffed from
	// the rows, so an empty table lays out like a full one.
	Numeric bool
	Scope   Scope
}

// Sheet is one table: its columns and their rows.
type Sheet struct {
	Key     string // JSON key: "rows", "repos", "movers"
	Columns []Column
	Rows    [][]Cell
	Scope   Scope
}

// columns returns the columns a format carries, in declared order.
func (s Sheet) columns(machine bool) []Column {
	out := make([]Column, 0, len(s.Columns))
	for _, c := range s.Columns {
		if (machine && c.Scope.inMachine()) || (!machine && c.Scope.inMarkdown()) {
			out = append(out, c)
		}
	}
	return out
}

// cells returns one row's values for the columns a format carries. A short
// row yields blanks rather than panicking.
func (s Sheet) cells(row []Cell, machine bool) []Cell {
	out := make([]Cell, 0, len(s.Columns))
	for i, c := range s.Columns {
		if (machine && !c.Scope.inMachine()) || (!machine && !c.Scope.inMarkdown()) {
			continue
		}
		if i < len(row) {
			out = append(out, row[i])
		} else {
			out = append(out, None())
		}
	}
	return out
}

// Meta is where the numbers came from. Markdown says the same in its
// heading.
type Meta struct {
	Team        string
	Window      *WindowMeta
	GeneratedAt time.Time // zero when unset; the TUI exports carry no clock
}

// WindowMeta is the window a view covers. Absent on trends and sync status.
type WindowMeta struct {
	Label string
	Start int64 // unix seconds UTC, inclusive
	End   int64 // unix seconds UTC, exclusive
}

// Note is a prose section under the tables. Markdown only: CSV and JSON
// carry the same facts as ScopeMachine columns. Item text is already
// sanitized and may carry Markdown emphasis.
type Note struct {
	Title string
	Items []NoteItem
}

// NoteItem is one bullet and its optional indented line.
type NoteItem struct {
	Text string
	Sub  string
}

// Doc is a whole view.
type Doc struct {
	View   string // "team" | "person" | "trends" | "sync"
	Title  string
	Lead   string // one paragraph under the title
	Meta   Meta
	Sheets []Sheet
	Notes  []Note
}

// sheets returns the sheets a format carries.
func (d Doc) sheets(machine bool) []Sheet {
	out := make([]Sheet, 0, len(d.Sheets))
	for _, s := range d.Sheets {
		if (machine && s.Scope.inMachine()) || (!machine && s.Scope.inMarkdown()) {
			out = append(out, s)
		}
	}
	return out
}

// plain renders a cell for the machine formats: durations in whole seconds,
// an empty string for no data. The JSON encoder checks the kind and writes
// null instead.
func (c Cell) plain() string {
	switch c.kind {
	case kindText:
		return strings.TrimSpace(text.Sanitize(c.text))
	case kindNum:
		return strconv.FormatInt(c.num, 10)
	case kindDur:
		return strconv.FormatInt(int64(c.dur/time.Second), 10)
	case kindBool:
		return strconv.FormatBool(c.flag)
	default:
		return ""
	}
}
