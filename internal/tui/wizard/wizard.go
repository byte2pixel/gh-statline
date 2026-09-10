// Package wizard is the setup flow: verify auth, pick an org, pick a team,
// review the imported members and repos, name the profile. Accounts
// without visible orgs or teams fall back to a manual form. It produces a
// config.Team, either as its own Bubble Tea program before the main app
// starts (first run, gh statline init) or embedded in the running app from
// the team switcher, where it reports its ending as a message instead of
// quitting.
package wizard

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

type step int

const (
	stepLoading step = iota
	stepOrg
	stepTeam
	stepManual
	stepReview
	stepName
)

// Model is the wizard's Bubble Tea model.
type Model struct {
	theme theme.Theme
	doer  gh.Doer

	step    step
	loading string // what the spinner is waiting on
	spin    spinner.Model

	login    string
	hasOrgs  bool
	orgs     list.Model
	teams    list.Model
	review   reviewList
	nameIn   textinput.Model
	existing map[string]bool

	// manual-entry form
	manOrg     textinput.Model
	manMembers textinput.Model
	manRepos   textinput.Model
	manFocus   int
	manNote    string
	manErr     string

	org  string
	slug string

	width, height int
	err           error
	// embedded marks a wizard hosted by the running app: it wears the
	// app's theme, leaves the terminal's background query to its host, and
	// ends with a DoneMsg rather than tea.Quit.
	embedded bool

	// Result is the completed team profile; nil if the user aborted.
	Result *config.Team
}

// DoneMsg is how an embedded wizard ends: the configured team, nil when
// the user backed out, or the error that stopped it. A wizard running as
// its own program quits instead and is read through Outcome.
type DoneMsg struct {
	Team *config.Team
	Err  error
}

// Embedded prepares the wizard to run inside the app with the app's
// palette. The host forwards it keys, resizes sized to the content area,
// and its own messages, and listens for DoneMsg.
func (m Model) Embedded(th theme.Theme) Model {
	m.embedded = true
	return m.WithTheme(th)
}

// WithTheme swaps the palette; the host calls it when the terminal answers
// the background query.
func (m Model) WithTheme(th theme.Theme) Model {
	m.theme = th
	m.spin.Style = lipgloss.NewStyle().Foreground(th.Accent)
	return m
}

// finish ends the wizard with its outcome: an embedded one reports to its
// host, a standalone one quits and is read through Outcome.
func (m Model) finish(team *config.Team, err error) (tea.Model, tea.Cmd) {
	m.Result, m.err = team, err
	if m.embedded {
		return m, func() tea.Msg { return DoneMsg{Team: team, Err: err} }
	}
	return m, tea.Quit
}

type orgItem string

func (o orgItem) FilterValue() string { return string(o) }
func (o orgItem) Title() string       { return string(o) }
func (o orgItem) Description() string { return "" }

type manualItem struct{}

func (manualItem) FilterValue() string { return "manual" }
func (manualItem) Title() string       { return "✎  Enter manually…" }
func (manualItem) Description() string { return "" }

type teamItem gh.TeamInfo

func (t teamItem) FilterValue() string { return t.Slug + " " + t.Name }
func (t teamItem) Title() string       { return t.Name }
func (t teamItem) Description() string { return t.Slug }

func New(doer gh.Doer, existingNames []string) Model {
	th := theme.New(true)
	m := Model{
		theme:    th,
		doer:     doer,
		step:     stepLoading,
		loading:  "checking GitHub auth…",
		spin:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		existing: map[string]bool{},
	}
	for _, n := range existingNames {
		m.existing[n] = true
	}
	m.spin.Style = lipgloss.NewStyle().Foreground(th.Accent)

	mk := func(ph string, w int) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = ph
		ti.SetWidth(w)
		return ti
	}
	m.nameIn = mk("team name", 30)
	m.nameIn.CharLimit = 40
	m.manOrg = mk("my-org (optional)", 40)
	m.manMembers = mk("alice bob carol", 60)
	m.manRepos = mk("owner/repo owner/other-repo", 60)
	return m
}

// Outcome reads a finished wizard: the configured team, nil when the user
// aborted, or the error that stopped it. The caller runs the model as a
// Bubble Tea program itself and hands the final model here.
func Outcome(final tea.Model) (*config.Team, error) {
	m, ok := final.(Model)
	if !ok {
		return nil, errors.New("unexpected wizard model")
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.Result, nil
}

type viewerMsg struct {
	login string
	orgs  []string
}
type teamsMsg []gh.TeamInfo
type teamsFailMsg struct{ err error }
type detailsMsg struct {
	members []string
	repos   []gh.TeamRepo
}
type failMsg struct{ err error }

func (m Model) Init() tea.Cmd {
	doer := m.doer
	cmds := []tea.Cmd{
		m.spin.Tick,
		func() tea.Msg {
			login, orgs, err := gh.Viewer(context.Background(), doer)
			if err != nil {
				return failMsg{err}
			}
			return viewerMsg{login: login, orgs: orgs}
		},
	}
	if !m.embedded { // the host has already asked, and owns the answer
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		if m.embedded {
			return m, nil // the host's palette wins; it re-themes the wizard itself
		}
		return m.WithTheme(theme.New(msg.IsDark())), nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.step == stepOrg {
			m.orgs.SetSize(msg.Width-4, m.listHeight())
		}
		if m.step == stepTeam {
			m.teams.SetSize(msg.Width-4, m.listHeight())
		}
		return m, nil

	case failMsg:
		return m.finish(nil, msg.err)

	case viewerMsg:
		m.login = msg.login
		m.hasOrgs = len(msg.orgs) > 0
		if !m.hasOrgs {
			return m.toManual("", fmt.Sprintf(
				"No organizations visible for %s — describe the team yourself.", m.login)), nil
		}
		items := make([]list.Item, 0, len(msg.orgs)+1)
		for _, o := range msg.orgs {
			items = append(items, orgItem(o))
		}
		items = append(items, manualItem{})
		m.orgs = m.newList(items, "Pick an organization", false)
		m.step = stepOrg
		return m, nil

	case teamsMsg:
		if len(msg) == 0 {
			return m.toManual(m.org, fmt.Sprintf(
				"No teams visible in %s — describe the team yourself.", m.org)), nil
		}
		items := make([]list.Item, len(msg))
		for i, t := range msg {
			items[i] = teamItem(t)
		}
		m.teams = m.newList(items, "Pick a team in "+m.org, true)
		m.step = stepTeam
		return m, nil

	case teamsFailMsg:
		return m.toManual(m.org, fmt.Sprintf(
			"Couldn't list teams in %s (%v) — describe the team yourself.", m.org, msg.err)), nil

	case detailsMsg:
		m.review = newReviewList(&m.theme, msg.members, msg.repos)
		m.step = stepReview
		return m, nil

	case spinner.TickMsg:
		if m.step != stepLoading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case list.FilterMatchesMsg:
		// The list filters asynchronously: filter keystrokes schedule a
		// fuzzy match whose results arrive as this message, which must
		// reach the list's Update or the visible items never narrow.
		var cmd tea.Cmd
		switch m.step {
		case stepOrg:
			m.orgs, cmd = m.orgs.Update(msg)
		case stepTeam:
			m.teams, cmd = m.teams.Update(msg)
		}
		return m, cmd

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// toManual routes to the manual-entry form, prefilled with org.
func (m Model) toManual(org, note string) Model {
	m.manOrg.SetValue(org)
	m.manNote = note
	m.manErr = ""
	m.manFocus = 0
	m.manOrg.Focus()
	m.manMembers.Blur()
	m.manRepos.Blur()
	m.slug = "" // manual profiles have no import provenance
	m.step = stepManual
	return m
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m.finish(nil, nil)
	}
	switch m.step {
	case stepLoading:
		// Only the opening query can be walked out of: the later loads
		// belong to a choice already made, and finish in a moment.
		if msg.String() == "esc" && m.login == "" {
			return m.finish(nil, nil)
		}
		return m, nil

	case stepOrg:
		if msg.String() == "esc" && m.orgs.FilterState() == list.Unfiltered {
			return m.finish(nil, nil) // the first step has nothing to go back to
		}
		if msg.String() == "enter" && m.orgs.FilterState() != list.Filtering {
			switch it := m.orgs.SelectedItem().(type) {
			case manualItem:
				return m.toManual("", "Describe the team yourself."), nil
			case orgItem:
				m.org = string(it)
				m.step = stepLoading
				m.loading = "fetching teams in " + m.org + "…"
				doer, org := m.doer, m.org
				return m, tea.Batch(m.spin.Tick, func() tea.Msg {
					teams, err := gh.OrgTeams(context.Background(), doer, org)
					if err != nil {
						return teamsFailMsg{err}
					}
					return teamsMsg(teams)
				})
			}
		}
		var cmd tea.Cmd
		m.orgs, cmd = m.orgs.Update(msg)
		return m, cmd

	case stepTeam:
		if m.teams.FilterState() != list.Filtering {
			switch msg.String() {
			case "esc":
				m.step = stepOrg
				return m, nil
			case "enter":
				if it, ok := m.teams.SelectedItem().(teamItem); ok {
					m.slug = it.Slug
					m.step = stepLoading
					m.loading = "importing " + m.org + "/" + m.slug + "…"
					doer, org, slug := m.doer, m.org, m.slug
					return m, tea.Batch(m.spin.Tick, func() tea.Msg {
						members, repos, err := gh.TeamDetails(context.Background(), doer, org, slug)
						if err != nil {
							return teamsFailMsg{err}
						}
						return detailsMsg{members: members, repos: repos}
					})
				}
			}
		}
		var cmd tea.Cmd
		m.teams, cmd = m.teams.Update(msg)
		return m, cmd

	case stepManual:
		return m.handleManualKey(msg)

	case stepReview:
		switch msg.String() {
		case "enter":
			m.nameIn.SetValue(m.uniqueName(m.defaultName()))
			m.nameIn.Focus()
			m.step = stepName
			return m, nil
		case "esc":
			if m.slug != "" {
				m.step = stepTeam
			} else {
				m.step = stepManual
			}
			return m, nil
		}
		m.review.handleKey(msg.String())
		return m, nil

	case stepName:
		switch msg.String() {
		case "enter":
			name := strings.TrimSpace(m.nameIn.Value())
			if name == "" || m.existing[name] {
				return m, nil // keep editing until it's valid and unique
			}
			team := m.review.toTeam(name, m.org, m.slug)
			return m.finish(&team, nil)
		case "esc":
			m.step = stepReview
			return m, nil
		}
		var cmd tea.Cmd
		m.nameIn, cmd = m.nameIn.Update(msg)
		return m, cmd
	}
	return m, nil
}

var splitRE = regexp.MustCompile(`[,\s]+`)

func (m Model) handleManualKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	inputs := []*textinput.Model{&m.manOrg, &m.manMembers, &m.manRepos}
	switch msg.String() {
	case "esc":
		if m.hasOrgs {
			m.step = stepOrg
			return m, nil
		}
		return m.finish(nil, nil)
	case "tab", "down":
		m.manFocus = (m.manFocus + 1) % len(inputs)
	case "shift+tab", "up":
		m.manFocus = (m.manFocus + len(inputs) - 1) % len(inputs)
	case "enter":
		if m.manFocus < len(inputs)-1 {
			m.manFocus++
			break
		}
		// Submit: parse members and repos, hand off to the review step.
		members := splitRE.Split(strings.TrimSpace(m.manMembers.Value()), -1)
		if len(members) == 1 && members[0] == "" {
			members = nil
		}
		var repos []gh.TeamRepo
		for _, tok := range splitRE.Split(strings.TrimSpace(m.manRepos.Value()), -1) {
			if tok == "" {
				continue
			}
			owner, name, ok := strings.Cut(tok, "/")
			if !ok || owner == "" || name == "" {
				m.manErr = fmt.Sprintf("%q is not owner/repo", tok)
				return m, nil
			}
			repos = append(repos, gh.TeamRepo{Owner: owner, Name: name})
		}
		if len(members) == 0 {
			m.manErr = "add at least one member login"
			m.manFocus = 1
			break
		}
		if len(repos) == 0 {
			m.manErr = "add at least one owner/repo"
			m.manFocus = 2
			break
		}
		m.manErr = ""
		m.org = strings.TrimSpace(m.manOrg.Value())
		m.review = newReviewList(&m.theme, members, repos)
		m.step = stepReview
		return m, nil
	default:
		var cmd tea.Cmd
		*inputs[m.manFocus], cmd = inputs[m.manFocus].Update(msg)
		return m, cmd
	}
	for i, in := range inputs {
		if i == m.manFocus {
			in.Focus()
		} else {
			in.Blur()
		}
	}
	return m, nil
}

// defaultName suggests a profile name: team slug, else org, else "team".
func (m Model) defaultName() string {
	if m.slug != "" {
		return m.slug
	}
	if m.org != "" {
		return m.org
	}
	return "team"
}

func (m Model) uniqueName(base string) string {
	if !m.existing[base] {
		return base
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", base, i); !m.existing[n] {
			return n
		}
	}
}

func (m Model) newList(items []list.Item, title string, showDesc bool) list.Model {
	d := list.NewDefaultDelegate()
	d.ShowDescription = showDesc
	l := list.New(items, d, m.width-4, m.listHeight())
	l.Title = title
	l.SetShowStatusBar(false)
	l.SetShowHelp(true)
	return l
}

func (m Model) listHeight() int {
	h := m.height - 4
	if h < 8 {
		h = 8
	}
	return h
}

func (m Model) View() tea.View {
	header := m.theme.Title.Render("Statline setup")
	if m.login != "" {
		header += m.theme.Header.Render("  authenticated as " + m.login)
	}

	var body string
	switch m.step {
	case stepLoading:
		body = "\n " + m.spin.View() + " " + m.loading
	case stepOrg:
		body = m.orgs.View()
	case stepTeam:
		body = m.teams.View()
	case stepManual:
		body = m.manualView()
	case stepReview:
		body = m.review.view(m.height - 4)
	case stepName:
		body = lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Bold(true).Foreground(m.theme.Primary).Render("Name this team profile"),
			"",
			m.nameIn.View(),
			"",
			m.theme.HelpDesc.Render("enter save · esc back"),
		)
	}

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, header, "", body))
	v.AltScreen = true
	return v
}

func (m Model) manualView() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Primary).Render("Describe the team")
	label := func(s string) string { return m.theme.HelpDesc.Render(s) }
	parts := []string{title}
	if m.manNote != "" {
		parts = append(parts, m.theme.Header.Render(m.manNote))
	}
	parts = append(parts, "",
		label("organization (optional, for display only)"),
		m.manOrg.View(),
		"",
		label("member logins (space or comma separated)"),
		m.manMembers.View(),
		"",
		label("repos as owner/name (space or comma separated)"),
		m.manRepos.View(),
	)
	if m.manErr != "" {
		parts = append(parts, "", lipgloss.NewStyle().Foreground(m.theme.Bad).Render(m.manErr))
	}
	parts = append(parts, "", m.theme.HelpDesc.Render("tab/enter next field · enter on last field continues · esc back"))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
