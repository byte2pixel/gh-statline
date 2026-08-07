// Package wizard is the first-run setup flow: verify auth, pick an org,
// pick a team, review the imported members and repos, name the profile.
// It runs as its own Bubble Tea program before the main app starts and
// produces a config.Team.
package wizard

import (
	"context"
	"errors"
	"fmt"
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
	orgs     list.Model
	teams    list.Model
	review   reviewList
	nameIn   textinput.Model
	existing map[string]bool

	org  string
	slug string

	width, height int
	err           error

	// Result is the completed team profile; nil if the user aborted.
	Result *config.Team
}

type orgItem string

func (o orgItem) FilterValue() string { return string(o) }
func (o orgItem) Title() string       { return string(o) }
func (o orgItem) Description() string { return "" }

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
	m.nameIn = textinput.New()
	m.nameIn.CharLimit = 40
	m.nameIn.SetWidth(30)
	return m
}

// Run executes the wizard and returns the configured team, or nil if the
// user aborted.
func Run(doer gh.Doer, existingNames []string) (*config.Team, error) {
	final, err := tea.NewProgram(New(doer, existingNames)).Run()
	if err != nil {
		return nil, err
	}
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
type detailsMsg struct {
	members []string
	repos   []gh.TeamRepo
}
type failMsg struct{ err error }

func (m Model) Init() tea.Cmd {
	doer := m.doer
	return tea.Batch(
		tea.RequestBackgroundColor,
		m.spin.Tick,
		func() tea.Msg {
			login, orgs, err := gh.Viewer(context.Background(), doer)
			if err != nil {
				return failMsg{err}
			}
			return viewerMsg{login: login, orgs: orgs}
		},
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.theme = theme.New(msg.IsDark())
		m.spin.Style = lipgloss.NewStyle().Foreground(m.theme.Accent)
		return m, nil

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
		m.err = msg.err
		return m, tea.Quit

	case viewerMsg:
		m.login = msg.login
		if len(msg.orgs) == 0 {
			m.err = fmt.Errorf("account %s belongs to no organizations visible with this token (need read:org scope)", m.login)
			return m, tea.Quit
		}
		items := make([]list.Item, len(msg.orgs))
		for i, o := range msg.orgs {
			items[i] = orgItem(o)
		}
		m.orgs = m.newList(items, "Pick an organization", false)
		m.step = stepOrg
		return m, nil

	case teamsMsg:
		if len(msg) == 0 {
			m.err = fmt.Errorf("no teams visible in %s (need read:org scope and team membership)", m.org)
			return m, tea.Quit
		}
		items := make([]list.Item, len(msg))
		for i, t := range msg {
			items[i] = teamItem(t)
		}
		m.teams = m.newList(items, "Pick a team in "+m.org, true)
		m.step = stepTeam
		return m, nil

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

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.Result = nil
		return m, tea.Quit
	}
	switch m.step {
	case stepOrg:
		if msg.String() == "enter" {
			if it, ok := m.orgs.SelectedItem().(orgItem); ok {
				m.org = string(it)
				m.step = stepLoading
				m.loading = "fetching teams in " + m.org + "…"
				doer, org := m.doer, m.org
				return m, tea.Batch(m.spin.Tick, func() tea.Msg {
					teams, err := gh.OrgTeams(context.Background(), doer, org)
					if err != nil {
						return failMsg{err}
					}
					return teamsMsg(teams)
				})
			}
		}
		var cmd tea.Cmd
		m.orgs, cmd = m.orgs.Update(msg)
		return m, cmd

	case stepTeam:
		if msg.String() == "enter" && m.teams.FilterState() != list.Filtering {
			if it, ok := m.teams.SelectedItem().(teamItem); ok {
				m.slug = it.Slug
				m.step = stepLoading
				m.loading = "importing " + m.org + "/" + m.slug + "…"
				doer, org, slug := m.doer, m.org, m.slug
				return m, tea.Batch(m.spin.Tick, func() tea.Msg {
					members, repos, err := gh.TeamDetails(context.Background(), doer, org, slug)
					if err != nil {
						return failMsg{err}
					}
					return detailsMsg{members: members, repos: repos}
				})
			}
		}
		var cmd tea.Cmd
		m.teams, cmd = m.teams.Update(msg)
		return m, cmd

	case stepReview:
		switch msg.String() {
		case "enter":
			m.nameIn.SetValue(m.uniqueName(m.slug))
			m.nameIn.Focus()
			m.step = stepName
			return m, nil
		case "esc":
			m.step = stepTeam
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
			m.Result = &team
			return m, tea.Quit
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
