package wizard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// scriptedDoer answers the wizard's three queries with canned payloads.
type scriptedDoer struct{}

func (scriptedDoer) DoWithContext(_ context.Context, query string, _ map[string]interface{}, resp interface{}) error {
	var payload string
	switch {
	case strings.Contains(query, "viewer"):
		payload = `{"viewer": {"login": "mel", "organizations": {"nodes": [{"login": "acme"}, {"login": "other-org"}]}}}`
	case strings.Contains(query, "teams(first"):
		payload = `{"organization": {"teams": {"nodes": [
			{"slug": "platform", "name": "Platform Eng"},
			{"slug": "design", "name": "Design"}
		]}}}`
	case strings.Contains(query, "team(slug"):
		payload = `{"organization": {"team": {
			"members": {"nodes": [{"login": "alice"}, {"login": "bob"}]},
			"repositories": {"nodes": [
				{"name": "api", "isArchived": false, "owner": {"login": "acme"}},
				{"name": "legacy", "isArchived": true, "owner": {"login": "acme"}}
			]}}}}`
	default:
		payload = `{}`
	}
	return json.Unmarshal([]byte(payload), resp)
}

// orglessDoer simulates a personal account with no org memberships.
type orglessDoer struct{}

func (orglessDoer) DoWithContext(_ context.Context, query string, _ map[string]interface{}, resp interface{}) error {
	if strings.Contains(query, "viewer") {
		return json.Unmarshal([]byte(`{"viewer": {"login": "solo", "organizations": {"nodes": []}}}`), resp)
	}
	return json.Unmarshal([]byte(`{}`), resp)
}

func TestWizardManualFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, New(orglessDoer{}, nil), teatest.WithInitialTermSize(100, 30))

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			return bytes.Contains(b, []byte(s))
		}, teatest.WithDuration(5*time.Second))
	}

	wait("Describe the team") // no orgs → straight to the manual form
	tm.Type("myspace")
	tm.Send(enter) // → members field
	tm.Type("alice, bob")
	tm.Send(enter) // → repos field
	tm.Type("acme/api charm/tea")
	tm.Send(enter) // submit → members
	wait("alice")
	tm.Send(enter) // accept the members → repos
	wait("charm/tea")
	tm.Send(enter) // accept the repos → name step
	wait("profile")
	tm.Send(enter) // default name = org ("myspace")

	result, err := Outcome(tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("wizard returned no team")
	}
	team := *result
	if team.Name != "myspace" || team.Org != "myspace" || team.GHTeamSlug != "" {
		t.Errorf("name/org/slug = %q/%q/%q", team.Name, team.Org, team.GHTeamSlug)
	}
	if len(team.Members) != 2 || team.Members[0].Login != "alice" || team.Members[1].Login != "bob" {
		t.Errorf("members = %+v", team.Members)
	}
	if len(team.Repos) != 2 || team.Repos[0].String() != "acme/api" || team.Repos[1].String() != "charm/tea" {
		t.Errorf("repos = %+v", team.Repos)
	}
}

func TestWizardFullFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, New(scriptedDoer{}, []string{"platform"}),
		teatest.WithInitialTermSize(100, 30))

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			return bytes.Contains(b, []byte(s))
		}, teatest.WithDuration(5*time.Second))
	}

	// Wait on strings unique to each step: the diff renderer may split
	// static prefixes across frames, so shared words never re-appear whole.
	wait("acme")
	tm.Send(enter) // acme
	wait("Platform Eng")
	tm.Send(enter) // platform
	wait("alice")
	// Exclude bob: move down once, toggle.
	tm.Send(tea.KeyPressMsg{Code: 'j', Text: "j"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	tm.Send(enter) // → repos
	wait("legacy")
	tm.Send(enter) // legacy stays unchecked → name step
	wait("profile")
	tm.Send(enter) // accept suggested unique name

	result, err := Outcome(tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("wizard returned no team")
	}
	team := *result
	if team.Name != "platform-2" { // "platform" already existed
		t.Errorf("name = %q, want platform-2", team.Name)
	}
	if team.Org != "acme" || team.GHTeamSlug != "platform" {
		t.Errorf("org/slug = %q/%q", team.Org, team.GHTeamSlug)
	}
	if len(team.Members) != 1 || team.Members[0].Login != "alice" {
		t.Errorf("members = %+v, want just alice (bob toggled off)", team.Members)
	}
	if len(team.Repos) != 1 || team.Repos[0].Name != "api" {
		t.Errorf("repos = %+v, want just acme/api (legacy is archived)", team.Repos)
	}
}

// runFilterCmds executes cmd, feeding any list.FilterMatchesMsg back into
// the model the way the runtime would. Every other message (blinks, ticks)
// is dropped to keep the test finite and deterministic.
func runFilterCmds(m tea.Model, cmd tea.Cmd) tea.Model {
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = runFilterCmds(m, c)
		}
	case list.FilterMatchesMsg:
		var next tea.Cmd
		m, next = m.Update(msg)
		return runFilterCmds(m, next)
	}
	return m
}

// TestWizardTeamFilter reproduces issue #15: typing into the `/` filter
// must narrow the team list, which only happens if the wizard routes the
// list's async FilterMatchesMsg back into it.
func TestWizardTeamFilter(t *testing.T) {
	var m tea.Model = New(scriptedDoer{}, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(teamsMsg{teams: []gh.TeamInfo{
		{Slug: "platform", Name: "Platform Eng"},
		{Slug: "design", Name: "Design"},
	}, total: 2})

	press := func(code rune, text string) {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyPressMsg{Code: code, Text: text})
		m = runFilterCmds(m, cmd)
	}
	press('/', "/")
	press('d', "d")
	press('e', "e")
	press('s', "s")

	if got := m.(Model).teams.VisibleItems(); len(got) != 1 {
		t.Fatalf("visible teams after filtering on \"des\" = %d, want 1", len(got))
	}

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	m, _ = m.Update(enter) // apply the filter
	m, _ = m.Update(enter) // select the surviving team
	if got := m.(Model).slug; got != "design" {
		t.Fatalf("selected slug = %q, want %q", got, "design")
	}
}

// Embedded in the app, the wizard reports its ending as a message instead
// of quitting the program: the team on success, nothing when the user
// backs out, the error when GitHub refused it.
func TestEmbeddedWizardReportsInsteadOfQuitting(t *testing.T) {
	th := theme.New(true)
	wiz := New(orglessDoer{}, nil).Embedded(th)
	done := func(cmd tea.Cmd) DoneMsg {
		t.Helper()
		if cmd == nil {
			t.Fatal("the ending produced no command")
		}
		msg, ok := cmd().(DoneMsg)
		if !ok {
			t.Fatalf("emitted %#v, want DoneMsg", cmd())
		}
		return msg
	}

	// Backing out of the manual form, the first step for an org-less account.
	m, _ := wiz.Update(viewerMsg{login: "solo"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if msg := done(cmd); msg.Team != nil || msg.Err != nil {
		t.Errorf("esc emitted %+v, want an empty DoneMsg", msg)
	}

	// Naming the profile.
	w := m.(Model)
	w = w.loadLists(gh.TeamImport{Members: []string{"alice"}, MembersTotal: 1})
	w.step = stepName
	w.nameIn.SetValue("fresh")
	_, cmd = w.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if msg := done(cmd); msg.Team == nil || msg.Team.Name != "fresh" || msg.Err != nil {
		t.Errorf("enter on the name emitted %+v, want the fresh team", msg)
	}

	// A failed query.
	_, cmd = wiz.Update(failMsg{errors.New("boom")})
	if msg := done(cmd); msg.Err == nil || msg.Team != nil {
		t.Errorf("a failure emitted %+v, want the error", msg)
	}

	// esc at the org list, the first step for everyone else. The list must
	// not be filtering, or esc belongs to it.
	m2, _ := New(scriptedDoer{}, nil).Embedded(th).Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m2, _ = m2.Update(viewerMsg{login: "mel", orgs: []string{"acme"}})
	_, cmd = m2.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if msg := done(cmd); msg.Team != nil || msg.Err != nil {
		t.Errorf("esc at the org list emitted %+v, want an empty DoneMsg", msg)
	}
}

// Standalone, the same endings still quit the program, so init and the
// first run read them through Outcome as before.
func TestStandaloneWizardStillQuits(t *testing.T) {
	m, _ := New(orglessDoer{}, nil).Update(viewerMsg{login: "solo"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc on the first step produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("standalone esc emitted %#v, want tea.QuitMsg", cmd())
	}
}

// Outcome is the only reader of the wizard's private error, so its three
// endings are pinned here: aborted, failed, and configured.
func TestOutcome(t *testing.T) {
	if _, err := Outcome(nil); err == nil {
		t.Error("a foreign final model was accepted")
	}
	if team, err := Outcome(Model{}); err != nil || team != nil {
		t.Errorf("aborted wizard = %v, %v; want nil, nil", team, err)
	}
	boom := errors.New("auth failed")
	if _, err := Outcome(Model{err: boom}); !errors.Is(err, boom) {
		t.Errorf("failed wizard err = %v, want %v", err, boom)
	}
	want := &config.Team{Name: "t"}
	if got, err := Outcome(Model{Result: want}); err != nil || got != want {
		t.Errorf("configured wizard = %v, %v; want the result", got, err)
	}
}

// pagedTeamDoer is a large org as the API answers it: one viewer org, 150
// teams over two pages, and a team whose 250 members span three pages and
// whose 150 repos span two. Every page carries totalCount and pageInfo;
// the cursor is the next page number. It answers any team slug with the
// same team.
type pagedTeamDoer struct{}

func (pagedTeamDoer) DoWithContext(_ context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	cursor, _ := vars["cursor"].(string)
	var payload string
	switch {
	case strings.Contains(query, "viewer"):
		payload = `{"viewer": {"login": "mel", "organizations": {"nodes": [{"login": "acme"}]}}}`
	case strings.Contains(query, "teams("):
		payload = pagedConnection([]string{"organization", "teams"}, cursor, 150, func(i int) string {
			if i == 1 {
				return `{"slug": "platform", "name": "Platform Eng"}`
			}
			return fmt.Sprintf(`{"slug": "t%03d", "name": "Team %03d"}`, i, i)
		})
	case strings.Contains(query, "members("):
		payload = pagedConnection([]string{"organization", "team", "members"}, cursor, 250, func(i int) string {
			return fmt.Sprintf(`{"login": "m%03d"}`, i)
		})
	case strings.Contains(query, "repositories("):
		payload = pagedConnection([]string{"organization", "team", "repositories"}, cursor, 150, func(i int) string {
			return fmt.Sprintf(`{"name": "r%03d", "isArchived": false, "owner": {"login": "acme"}}`, i)
		})
	default:
		payload = `{}`
	}
	return json.Unmarshal([]byte(payload), resp)
}

// pagedConnection renders one page of 100 out of total nodes, numbered
// from 1, nested under path.
func pagedConnection(path []string, cursor string, total int, node func(i int) string) string {
	page, _ := strconv.Atoi(cursor)
	start, end := page*100, min(page*100+100, total)
	nodes := make([]string, 0, end-start)
	for i := start + 1; i <= end; i++ {
		nodes = append(nodes, node(i))
	}
	next := ""
	if end < total {
		next = strconv.Itoa(page + 1)
	}
	conn := fmt.Sprintf(`{"totalCount": %d, "pageInfo": {"hasNextPage": %v, "endCursor": %q}, "nodes": [%s]}`,
		total, next != "", next, strings.Join(nodes, ", "))
	for i := len(path) - 1; i >= 0; i-- {
		conn = fmt.Sprintf(`{%q: %s}`, path[i], conn)
	}
	return conn
}

// runCmd executes cmd, flattening batches, and returns the first message
// of type T it produced. Other messages (spinner ticks) are dropped.
func runCmd[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	var found *T
	var walk func(tea.Cmd)
	walk = func(cmd tea.Cmd) {
		if cmd == nil || found != nil {
			return
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				walk(c)
			}
		case T:
			found = &msg
		}
	}
	walk(cmd)
	if found == nil {
		var zero T
		t.Fatalf("the command produced no %T", zero)
	}
	return *found
}

func plainView(m tea.Model) string { return ansi.Strip(m.View().Content) }

// TestWizardImportsWholeTeam is the PRD success metric "whole-team import":
// a team bigger than one API page arrives complete, in API order, and the
// review step says how many of each it holds (#103).
func TestWizardImportsWholeTeam(t *testing.T) {
	var m tea.Model = New(pagedTeamDoer{}, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(viewerMsg{login: "mel", orgs: []string{"acme"}})

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	var cmd tea.Cmd
	m, cmd = m.Update(enter) // acme
	teams := runCmd[teamsMsg](t, cmd)
	if len(teams.teams) != 150 || teams.total != 150 {
		t.Fatalf("teams = %d of %d, want 150 of 150", len(teams.teams), teams.total)
	}
	m, _ = m.Update(teams)
	if v := plainView(m); !strings.Contains(v, "Pick a team in acme (150)") {
		t.Errorf("team list title lacks the count:\n%s", v)
	}

	m, cmd = m.Update(enter) // platform, the first team
	details := runCmd[detailsMsg](t, cmd)
	m, _ = m.Update(details)

	w := m.(Model)
	if w.step != stepMembers || w.members.Len() != 250 || w.repos.Len() != 150 {
		t.Fatalf("step = %v listing %d members and %d repos, want the members step over 250 and 150",
			w.step, w.members.Len(), w.repos.Len())
	}
	if v := plainView(m); !strings.Contains(v, "Members · 250 of 250 included") || strings.Contains(v, "Showing") {
		t.Errorf("members step should count 250 of 250 and report no cut:\n%s", v)
	}
	m, _ = m.Update(enter)
	if v := plainView(m); !strings.Contains(v, "Repos · 150 of 150 included") || strings.Contains(v, "Showing") {
		t.Errorf("repos step should count 150 of 150 and report no cut:\n%s", v)
	}
	team := w.toTeam("platform")
	if team.GHTeamSlug != "platform" || team.Org != "acme" {
		t.Errorf("org/slug = %q/%q", team.Org, team.GHTeamSlug)
	}
	if n := len(team.Members); n != 250 || team.Members[0].Login != "m001" || team.Members[n-1].Login != "m250" {
		t.Errorf("members = %d, first %q, last %q; want 250 from m001 to m250", n, team.Members[0].Login, team.Members[n-1].Login)
	}
	if n := len(team.Repos); n != 150 || team.Repos[0].String() != "acme/r001" || team.Repos[n-1].String() != "acme/r150" {
		t.Errorf("repos = %d, first %q, last %q; want 150 from acme/r001 to acme/r150", n, team.Repos[0], team.Repos[n-1])
	}
}

// TestWizardSaysWhenAListWasCut is the PRD success metric "truncation
// honesty" for the team list: when fewer teams arrived than the API
// counted, a note under the step title says so, and the heading counts
// what is actually there (#103). The member and repo lists are covered by
// TestWizardShowsTheCutNoticeOnTheStep.
func TestWizardSaysWhenAListWasCut(t *testing.T) {
	var m tea.Model = New(scriptedDoer{}, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	w := m.(Model)
	w.login, w.org = "mel", "acme"
	m = w

	teams := make([]gh.TeamInfo, 5000)
	for i := range teams {
		teams[i] = gh.TeamInfo{Slug: fmt.Sprintf("t%04d", i), Name: fmt.Sprintf("Team %04d", i)}
	}
	m, _ = m.Update(teamsMsg{teams: teams, total: 5210})
	v := plainView(m)
	for _, want := range []string{
		"Pick a team in acme (5210)",
		"Showing 5000 of 5210 teams: the list was cut.",
		"Enter manually",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("cut team list lacks %q:\n%s", want, v)
		}
	}

}

// TestPickerKeysNeverQuit pins the fix for the v key. bubbles binds its
// list quit key to v and labels it "select", so with the default keymap
// the pickers advertised v and then quit on it: init ended with "setup
// aborted" and, embedded, the whole app exited. Neither picker may emit
// a quit for a letter, and the help under them names the keys the wizard
// actually handles.
func TestPickerKeysNeverQuit(t *testing.T) {
	th := theme.New(true)
	for _, tc := range []struct {
		name string
		mk   func() tea.Model
		esc  string
	}{
		{"standalone", func() tea.Model { return New(scriptedDoer{}, nil) }, "esc quit"},
		{"embedded", func() tea.Model { return New(scriptedDoer{}, nil).Embedded(th) }, "esc back"},
	} {
		m := tc.mk()
		m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m, _ = m.Update(viewerMsg{login: "mel", orgs: []string{"acme"}})
		assertPickerStays(t, tc.name+" org picker", m, stepOrg, tc.esc)
		m, _ = m.Update(teamsMsg{teams: []gh.TeamInfo{{Slug: "platform", Name: "Platform Eng"}}, total: 1})
		assertPickerStays(t, tc.name+" team picker", m, stepTeam, "esc back")
	}
}

func assertPickerStays(t *testing.T, name string, m tea.Model, want step, esc string) {
	t.Helper()
	v := plainView(m)
	if strings.Contains(v, "v select") || !strings.Contains(v, "enter select") || !strings.Contains(v, esc) {
		t.Errorf("%s help does not describe the wizard keys (want enter select and %s):\n%s", name, esc, v)
	}
	for _, k := range []rune{'v', 'q'} {
		next, cmd := m.Update(tea.KeyPressMsg{Code: k, Text: string(k)})
		if quits(cmd) {
			t.Errorf("%s: %c quit the wizard", name, k)
		}
		if got := next.(Model).step; got != want {
			t.Errorf("%s: %c moved the step to %v", name, k, got)
		}
	}
}

// quits reports whether cmd, flattened, produces a tea.QuitMsg.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if quits(c) {
				return true
			}
		}
	}
	return false
}

// failingDetailsDoer lists teams like scriptedDoer but fails the import.
type failingDetailsDoer struct{}

func (failingDetailsDoer) DoWithContext(ctx context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	if strings.Contains(query, "team(slug") {
		return errors.New("502 bad gateway")
	}
	return scriptedDoer{}.DoWithContext(ctx, query, vars, resp)
}

// A failed import says it was the import that failed, naming the team,
// not the team list, which had already loaded.
func TestImportFailureNamesTheImport(t *testing.T) {
	var m tea.Model = New(failingDetailsDoer{}, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(viewerMsg{login: "mel", orgs: []string{"acme"}})
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	var cmd tea.Cmd
	m, cmd = m.Update(enter) // acme
	m, _ = m.Update(runCmd[teamsMsg](t, cmd))
	m, cmd = m.Update(enter) // platform
	m, _ = m.Update(runCmd[detailsFailMsg](t, cmd))
	w := m.(Model)
	if w.step != stepManual {
		t.Fatalf("step = %v after a failed import, want the manual form", w.step)
	}
	if !strings.Contains(w.manNote, "import acme/platform") || strings.Contains(w.manNote, "list teams") || !strings.Contains(w.manNote, "502") {
		t.Errorf("manual note = %q, want it to name the failed import of acme/platform with the error", w.manNote)
	}
	if v := plainView(m); !strings.Contains(v, "acme/platform") {
		t.Errorf("the manual form does not show the note:\n%s", v)
	}
}

// A resize reaches every picker, not only the one on screen, so backing
// out of the members step to the team picker after a resize finds it sized to
// the terminal; and a picker with a note still fits the frame.
func TestResizeReachesEveryPicker(t *testing.T) {
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	teams := []gh.TeamInfo{{Slug: "platform", Name: "Platform Eng"}}
	fresh := func(h int) Model {
		var m tea.Model = New(scriptedDoer{}, nil)
		m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: h})
		m, _ = m.Update(viewerMsg{login: "mel", orgs: []string{"acme"}})
		m, _ = m.Update(teamsMsg{teams: teams, total: 1})
		return m.(Model)
	}
	want := fresh(50)

	var m tea.Model = fresh(30)
	var cmd tea.Cmd
	m, cmd = m.Update(enter) // platform
	m, _ = m.Update(runCmd[detailsMsg](t, cmd))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 50}) // resized on the members step
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})      // back to the team picker
	w := m.(Model)
	if w.step != stepTeam {
		t.Fatalf("step = %v, want the team picker", w.step)
	}
	if w.teams.Height() != want.teams.Height() || w.orgs.Height() != want.orgs.Height() {
		t.Errorf("after a resize on the review, teams/orgs are %d/%d rows; pickers sized at 50 rows are %d/%d",
			w.teams.Height(), w.orgs.Height(), want.teams.Height(), want.orgs.Height())
	}
	if got := lipgloss.Height(m.View().Content); got > 50 {
		t.Errorf("team picker frame is %d rows in a 50-row terminal", got)
	}

	// A cut list adds a note; the picker gives up the rows so the frame fits.
	many := make([]gh.TeamInfo, 200)
	for i := range many {
		many[i] = gh.TeamInfo{Slug: fmt.Sprintf("t%03d", i), Name: fmt.Sprintf("Team %03d", i)}
	}
	m, _ = m.Update(teamsMsg{teams: many, total: 5210})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	if got := lipgloss.Height(m.View().Content); got > 24 {
		t.Errorf("cut team picker frame is %d rows in a 24-row terminal:\n%s", got, plainView(m))
	}
}

var (
	kEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	kEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	kDown  = tea.KeyPressMsg{Code: 'j', Text: "j"}
	kSlash = tea.KeyPressMsg{Code: '/', Text: "/"}
	kAll   = tea.KeyPressMsg{Code: 'a', Text: "a"}
	kNone  = tea.KeyPressMsg{Code: 'n', Text: "n"}
)

// importedAtMembers runs the wizard over doer through the org and team
// pickers, taking the first of each, to the members step of the import,
// on a terminal h rows tall.
func importedAtMembers(t *testing.T, doer gh.Doer, h int) tea.Model {
	t.Helper()
	var m tea.Model = New(doer, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: h})
	m, _ = m.Update(viewerMsg{login: "mel", orgs: []string{"acme"}})
	var cmd tea.Cmd
	m, cmd = m.Update(kEnter) // acme
	m, _ = m.Update(runCmd[teamsMsg](t, cmd))
	m, cmd = m.Update(kEnter) // the first team
	m, _ = m.Update(runCmd[detailsMsg](t, cmd))
	return m
}

// manualAtMembers submits the manual form with members and repos typed
// in, which lands on the members step of a manual profile.
func manualAtMembers(t *testing.T, members, repos string) tea.Model {
	t.Helper()
	var m tea.Model = New(orglessDoer{}, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(viewerMsg{login: "solo"})
	w := m.(Model)
	w.manMembers.SetValue(members)
	w.manRepos.SetValue(repos)
	w.manFocus = 2
	m, _ = w.Update(kEnter)
	return m
}

func keys(m tea.Model, ks ...tea.KeyPressMsg) tea.Model {
	for _, k := range ks {
		m, _ = m.Update(k)
	}
	return m
}

func typeKeys(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func stepOf(m tea.Model) step { return m.(Model).step }

// TestWizardRepoStepCannotBeSkipped is the PRD success metric "repo step
// cannot be skipped": the members and the repos are confirmed on separate
// steps, each headed by its count, and enter on the members lands on the
// repos rather than on the name. Archived repos start unchecked, so the
// import lists 1 of 2 (#104).
func TestWizardRepoStepCannotBeSkipped(t *testing.T) {
	for _, tc := range []struct {
		name  string
		m     tea.Model
		repos string
	}{
		{"imported", importedAtMembers(t, scriptedDoer{}, 30), "Repos · 1 of 2 included"},
		{"manual", manualAtMembers(t, "alice bob", "acme/api charm/tea"), "Repos · 2 of 2 included"},
	} {
		m := tc.m
		if stepOf(m) != stepMembers {
			t.Fatalf("%s: step = %v, want the members step", tc.name, stepOf(m))
		}
		if v := plainView(m); !strings.Contains(v, "Members · 2 of 2 included") || !strings.Contains(v, "enter → repos") {
			t.Errorf("%s: members step lacks its count or footer:\n%s", tc.name, v)
		}
		m = keys(m, kEnter)
		if stepOf(m) != stepRepos {
			t.Fatalf("%s: enter on the members went to step %v, want the repos", tc.name, stepOf(m))
		}
		if v := plainView(m); !strings.Contains(v, tc.repos) || !strings.Contains(v, "enter → name the profile") {
			t.Errorf("%s: repos step lacks %q or its footer:\n%s", tc.name, tc.repos, v)
		}
		if tc.name == "imported" {
			if v := plainView(m); !strings.Contains(v, "[x] acme/api") || !strings.Contains(v, "[ ] acme/legacy") {
				t.Errorf("archived legacy should start unchecked beside api:\n%s", v)
			}
		}
		m = keys(m, kEnter)
		if stepOf(m) != stepName {
			t.Errorf("%s: enter on the repos went to step %v, want the name", tc.name, stepOf(m))
		}
	}
}

// esc walks back one step at a time: name to repos, repos to members, and
// members to wherever the lists came from, the team picker or the form.
func TestWizardStepsBackThroughBothLists(t *testing.T) {
	m := keys(importedAtMembers(t, scriptedDoer{}, 30), kEnter, kEnter)
	if stepOf(m) != stepName {
		t.Fatalf("step = %v, want the name step", stepOf(m))
	}
	for _, want := range []step{stepRepos, stepMembers, stepTeam} {
		if m = keys(m, kEsc); stepOf(m) != want {
			t.Fatalf("esc went to step %v, want %v", stepOf(m), want)
		}
	}
	if m = keys(manualAtMembers(t, "alice", "acme/api"), kEsc); stepOf(m) != stepManual {
		t.Errorf("esc on a manual profile went to step %v, want the form", stepOf(m))
	}
}

// A team with no members answers nothing, so enter on an empty members
// list is refused with a note, the way the member picker refuses to hide
// everyone, and the next key clears it.
func TestWizardRefusesAnEmptyMemberList(t *testing.T) {
	m := keys(importedAtMembers(t, scriptedDoer{}, 30), kNone, kEnter)
	if stepOf(m) != stepMembers {
		t.Fatalf("enter with nobody included went to step %v", stepOf(m))
	}
	if v := plainView(m); !strings.Contains(v, "keep at least one member") || !strings.Contains(v, "Members · 0 of 2 included") {
		t.Errorf("refusal note or count missing:\n%s", v)
	}
	m = keys(m, kAll)
	if v := plainView(m); strings.Contains(v, "keep at least") {
		t.Errorf("note not cleared by the next key:\n%s", v)
	}
	if m = keys(m, kEnter); stepOf(m) != stepRepos {
		t.Errorf("enter with everyone back went to step %v, want the repos", stepOf(m))
	}
}

// An org team with no assigned repos is a real onboarding case, so an
// empty repos list is allowed: enter warns that nothing will sync, any
// other key withdraws the warning, and a second enter in a row goes on.
// The profile saved that way has its members and no repos.
func TestWizardWarnsThenAllowsNoRepos(t *testing.T) {
	m := keys(importedAtMembers(t, scriptedDoer{}, 30), kEnter, kNone, kEnter)
	if stepOf(m) != stepRepos {
		t.Fatalf("the first enter with no repos went to step %v", stepOf(m))
	}
	if v := plainView(m); !strings.Contains(v, "no repos included") || !strings.Contains(v, "enter again") {
		t.Errorf("warning missing:\n%s", v)
	}
	m = keys(m, kDown, kEnter) // j withdraws the warning; enter has to ask again
	if v := plainView(m); stepOf(m) != stepRepos || !strings.Contains(v, "no repos included") {
		t.Fatalf("enter after another key should warn again, got step %v:\n%s", stepOf(m), v)
	}
	if m = keys(m, kEnter); stepOf(m) != stepName {
		t.Fatalf("the second enter went to step %v, want the name", stepOf(m))
	}
	m = keys(m, kEnter) // the suggested name
	w := m.(Model)
	if w.Result == nil || len(w.Result.Repos) != 0 || len(w.Result.Members) != 2 {
		t.Errorf("saved %+v, want alice and bob with no repos", w.Result)
	}
}

// Both steps are the same list the app pickers use, so / narrows a big
// team and a and n act on what the query shows.
func TestWizardListsSearchAndBulkToggle(t *testing.T) {
	m := keys(importedAtMembers(t, pagedTeamDoer{}, 30), kSlash)
	m = keys(typeKeys(m, "m00"), kEnter, kNone) // m001 to m009
	if v := plainView(m); !strings.Contains(v, "Members · 241 of 250 included") {
		t.Errorf("n under a query should uncheck the nine matches:\n%s", v)
	}
	m = keys(m, kEsc) // clears the query, stays on the step
	if stepOf(m) != stepMembers {
		t.Fatalf("esc with a query went to step %v", stepOf(m))
	}
	m = keys(m, kEnter, kSlash)
	m = keys(typeKeys(m, "r00"), kEnter, kNone) // r001 to r009
	if v := plainView(m); stepOf(m) != stepRepos || !strings.Contains(v, "Repos · 141 of 150 included") {
		t.Errorf("n under a query on the repos should uncheck the nine matches:\n%s", v)
	}
}

// TestWizardShowsTheCutNoticeOnTheStep is the PRD success metric
// "truncation honesty" for the import: a cut member list is reported
// above the members, a cut repo list above the repos, each with the count
// the API gave and where the rest go, and a complete import or a manual
// profile says nothing (#103, #104).
func TestWizardShowsTheCutNoticeOnTheStep(t *testing.T) {
	var m tea.Model = New(scriptedDoer{}, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	w := m.(Model)
	w.login, w.org, w.slug = "mel", "acme", "platform"
	m = w

	members := make([]string, 1000)
	for i := range members {
		members[i] = fmt.Sprintf("m%04d", i)
	}
	repos := make([]gh.TeamRepo, 150)
	for i := range repos {
		repos[i] = gh.TeamRepo{Owner: "acme", Name: fmt.Sprintf("r%03d", i)}
	}
	check := func(name, v, note, heading string) {
		t.Helper()
		if note == "" {
			if strings.Contains(v, "Showing") {
				t.Errorf("%s: a complete list reported a cut:\n%s", name, v)
			}
			return
		}
		n, h := strings.Index(v, note), strings.Index(v, heading)
		if n < 0 || h < 0 || n > h {
			t.Errorf("%s: want %q above %q:\n%s", name, note, heading, v)
		}
		if !strings.Contains(v, "config.yml") {
			t.Errorf("%s: the note does not say where the rest go", name)
		}
	}
	cases := []struct {
		membersTotal, reposTotal int
		membersNote, reposNote   string
	}{
		{1437, 150, "Showing 1000 of 1437 members: the list was cut.", ""},
		{1000, 151, "", "Showing 150 of 151 repos: the list was cut."},
		{1437, 151, "Showing 1000 of 1437 members: the list was cut.", "Showing 150 of 151 repos: the list was cut."},
		{1000, 150, "", ""},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("totals %d/%d", tc.membersTotal, tc.reposTotal)
		m, _ = m.Update(detailsMsg{Members: members, MembersTotal: tc.membersTotal, Repos: repos, ReposTotal: tc.reposTotal})
		check(name+" members", plainView(m), tc.membersNote, "Members · 1000 of 1000 included")
		m = keys(m, kEnter)
		check(name+" repos", plainView(m), tc.reposNote, "Repos · 150 of 150 included")
	}

	// The manual path never imported anything, so it never reports a cut,
	// even after a cut import was abandoned for it.
	m, _ = m.Update(detailsMsg{Members: members, MembersTotal: 1437, Repos: repos, ReposTotal: 151})
	w = m.(Model).toManual("acme", "Describe the team yourself.")
	w = w.loadLists(gh.TeamImport{Members: []string{"alice"}, MembersTotal: 1, Repos: repos[:1], ReposTotal: 1})
	w.step = stepMembers
	check("manual members", plainView(w), "", "")
	w.step = stepRepos
	check("manual repos", plainView(w), "", "")
}

// The boxed list fits under the wizard header on a short terminal, with
// a query line open and with a cut note above it, on both steps.
func TestWizardListsFitTheTerminal(t *testing.T) {
	members := make([]string, 1000)
	for i := range members {
		members[i] = fmt.Sprintf("m%04d", i)
	}
	repos := make([]gh.TeamRepo, 150)
	for i := range repos {
		repos[i] = gh.TeamRepo{Owner: "acme", Name: fmt.Sprintf("r%03d", i)}
	}
	for _, h := range []int{24, 30} {
		fits := func(state string, m tea.Model) {
			t.Helper()
			if got := lipgloss.Height(m.View().Content); got > h {
				t.Errorf("%d rows, %s: the frame is %d rows:\n%s", h, state, got, plainView(m))
			}
		}
		m := importedAtMembers(t, pagedTeamDoer{}, h)
		fits("members", m)
		fits("members with a query", typeKeys(keys(m, kSlash), "m0"))
		m = keys(m, kEnter)
		fits("repos", m)
		fits("repos with a query", typeKeys(keys(m, kSlash), "r0"))

		m, _ = m.Update(detailsMsg{Members: members, MembersTotal: 1437, Repos: repos, ReposTotal: 151})
		fits("cut members", m)
		fits("cut repos", keys(m, kEnter))
		m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: h - 4}) // and after shrinking on the step
		if got := lipgloss.Height(m.View().Content); got > h-4 {
			t.Errorf("%d rows after shrinking: the frame is %d rows", h-4, got)
		}
	}
}
