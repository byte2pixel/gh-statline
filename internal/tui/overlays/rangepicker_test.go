package overlays

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// ansiRE strips color escapes so a substring check cannot match across a
// sequence boundary.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plainView(p RangePicker) string { return ansiRE.ReplaceAllString(p.View(), "") }

func newPicker() RangePicker {
	th := theme.New(true)
	return NewRangePicker(&th)
}

// send delivers one key and returns the picker plus whatever message its
// command produced, or nil when it produced none.
func send(p RangePicker, key tea.KeyPressMsg) (RangePicker, tea.Msg) {
	p, cmd := p.Update(key)
	if cmd == nil {
		return p, nil
	}
	return p, cmd()
}

// typeText enters a date the way a user would, one keypress at a time. The
// commands are dropped rather than run: a keystroke into a text input
// returns the cursor-blink tick, and running it would sleep the test.
func typeText(p RangePicker, s string) RangePicker {
	for _, r := range s {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return p
}

var (
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyTab   = tea.KeyPressMsg{Code: tea.KeyTab}
)

// enterRange types both dates and confirms, returning the message the picker
// emitted. Enter on the first field only advances focus, so it takes two.
func enterRange(p RangePicker, from, to string) (RangePicker, tea.Msg) {
	p = typeText(p, from)
	p, msg := send(p, keyEnter)
	if msg != nil {
		return p, msg
	}
	p = typeText(p, to)
	return send(p, keyEnter)
}

// Windows are UTC days and typed dates are parsed as UTC, so the placeholder
// hints have to be UTC too — near midnight a local-time hint names a day the
// picker would not produce.
func TestNewRangePickerHintsUTCDates(t *testing.T) {
	// The picker reads its own clock, so bracket the construction and accept
	// either side. Comparing against a single later reading fails whenever
	// the run straddles UTC midnight: rare, once a day, and says nothing
	// about the code.
	before := time.Now().UTC()
	p := newPicker()
	after := time.Now().UTC()

	// wantDay checks a placeholder against the day months from now, as UTC
	// saw it on either side of the construction.
	wantDay := func(field, got string, months int) {
		t.Helper()
		for _, base := range []time.Time{before, after} {
			if got == base.AddDate(0, months, 0).Format(dateLayout) {
				return
			}
		}
		t.Errorf("%s placeholder = %q, want %q in UTC", field, got,
			before.AddDate(0, months, 0).Format(dateLayout))
	}
	wantDay("to", p.to.Placeholder, 0)
	wantDay("from", p.from.Placeholder, -3)
	if !p.from.Focused() {
		t.Error("the from field should be focused on open")
	}
	if p.focus != 0 {
		t.Errorf("focus = %d, want the from field", p.focus)
	}
}

func TestRangePickerEscCancels(t *testing.T) {
	_, msg := send(newPicker(), keyEsc)
	if _, ok := msg.(RangeCancelledMsg); !ok {
		t.Errorf("esc emitted %#v, want RangeCancelledMsg", msg)
	}
}

// Enter on the from field advances rather than submitting: the to field is
// still empty, and submitting there would just raise a parse error.
func TestRangePickerEnterAdvancesFromTheFirstField(t *testing.T) {
	p, msg := send(typeText(newPicker(), "2026-01-01"), keyEnter)
	if msg != nil {
		t.Fatalf("enter on the from field emitted %#v, want nothing yet", msg)
	}
	if p.focus != 1 || !p.to.Focused() || p.from.Focused() {
		t.Errorf("focus = %d (from focused %v, to focused %v), want the to field",
			p.focus, p.from.Focused(), p.to.Focused())
	}
}

func TestRangePickerTabSwapsFocusBothWays(t *testing.T) {
	p, _ := send(newPicker(), keyTab)
	if p.focus != 1 || !p.to.Focused() {
		t.Fatalf("tab left focus at %d", p.focus)
	}
	p, _ = send(p, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if p.focus != 0 || !p.from.Focused() {
		t.Errorf("shift+tab left focus at %d, want the from field", p.focus)
	}
}

// A confirmed range is parsed as UTC and handed over as whole days, which is
// what metrics.Window expects.
func TestRangePickerConfirmsAUTCRange(t *testing.T) {
	_, msg := enterRange(newPicker(), "2026-01-01", "2026-03-15")
	chosen, ok := msg.(RangeChosenMsg)
	if !ok {
		t.Fatalf("emitted %#v, want RangeChosenMsg", msg)
	}
	wantFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	if !chosen.From.Equal(wantFrom) || !chosen.To.Equal(wantTo) {
		t.Errorf("range = %v..%v, want %v..%v", chosen.From, chosen.To, wantFrom, wantTo)
	}
	if chosen.From.Location() != time.UTC || chosen.To.Location() != time.UTC {
		t.Errorf("dates parsed in %v/%v, want UTC", chosen.From.Location(), chosen.To.Location())
	}
}

// Both validation failures keep the modal open with an explanation, rather
// than emitting a range the rest of the app would have to reject.
func TestRangePickerRejectsBadInput(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		wantErr  string
	}{
		{"unparseable", "yesterday", "today", "use YYYY-MM-DD"},
		{"wrong separator", "2026/01/01", "2026/03/15", "use YYYY-MM-DD"},
		{"impossible date", "2026-02-30", "2026-03-15", "use YYYY-MM-DD"},
		{"only one filled in", "2026-01-01", "", "use YYYY-MM-DD"},
		{"reversed", "2026-03-15", "2026-01-01", "is before"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, msg := enterRange(newPicker(), c.from, c.to)
			if msg != nil {
				t.Fatalf("emitted %#v, want the modal to stay open", msg)
			}
			if !strings.Contains(p.err, c.wantErr) {
				t.Errorf("err = %q, want it to mention %q", p.err, c.wantErr)
			}
			if !strings.Contains(plainView(p), p.err) {
				t.Errorf("the error is not shown in the modal:\n%s", plainView(p))
			}
		})
	}
}

// Moving between fields is how a user fixes a rejected range, so the stale
// complaint has to clear rather than sit under the corrected input.
func TestRangePickerClearsTheErrorOnFocusChange(t *testing.T) {
	p, _ := enterRange(newPicker(), "2026-03-15", "2026-01-01")
	if p.err == "" {
		t.Fatal("expected a validation error to clear")
	}
	p, _ = send(p, keyTab)
	if p.err != "" {
		t.Errorf("err = %q after tab, want it cleared", p.err)
	}
}

func TestRangePickerViewShowsBothFieldsAndKeys(t *testing.T) {
	v := plainView(newPicker())
	for _, want := range []string{"Custom range", "from", "to", "enter apply", "tab switch", "esc cancel"} {
		if !strings.Contains(v, want) {
			t.Errorf("view is missing %q:\n%s", want, v)
		}
	}
}
