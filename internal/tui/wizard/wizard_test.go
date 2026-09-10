package wizard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
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
	tm.Send(enter) // submit → review
	wait("alice")
	tm.Send(enter) // accept all → name step
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
	tm.Send(enter)
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
	m, _ = m.Update(teamsMsg{
		{Slug: "platform", Name: "Platform Eng"},
		{Slug: "design", Name: "Design"},
	})

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
	w.review = newReviewList(&w.theme, []string{"alice"}, nil)
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
