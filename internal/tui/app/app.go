// Package app hosts Statline's root Bubble Tea model: routing, global keys,
// the sync-event bridge, and layout composition.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/syncer"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/pages"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// Deps is everything the TUI needs from the outside world, prepared by the
// root command before the program starts.
type Deps struct {
	DB      *sql.DB
	Store   *db.Store
	Cfg     config.Config
	Team    config.Team
	TeamID  int64
	Targets []syncer.Target
	Doer    gh.Doer
}

var windowPresets = []int{7, 14, 30, 90}

type Model struct {
	deps  Deps
	theme theme.Theme
	keys  keys.KeyMap
	help  help.Model
	spin  spinner.Model
	board pages.Leaderboard
	bots  *config.BotMatcher

	winIdx int
	window metrics.Window

	width, height int
	syncing       bool
	syncStatus    string
	lastSyncDone  time.Time
	err           error
}

func New(deps Deps) Model {
	th := theme.New(true) // corrected on the BackgroundColorMsg that follows Init
	km := keys.Default()

	m := Model{
		deps:   deps,
		theme:  th,
		keys:   km,
		help:   help.New(),
		spin:   spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		bots:   config.NewBotMatcher(deps.Cfg.ExcludeBots),
		winIdx: presetIndex(deps.Cfg.UI.Window),
	}
	m.window = metrics.LastDays(windowPresets[m.winIdx])
	m.board = pages.NewLeaderboard(&m.theme, km, deps.Cfg.UI.Sort)
	m.spin.Style = lipgloss.NewStyle().Foreground(th.Accent)
	return m
}

func presetIndex(w string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(w, "d"))
	for i, p := range windowPresets {
		if p == n {
			return i
		}
	}
	return 2 // 30d
}

// Messages internal to the app.
type dataMsg struct {
	rows []metrics.Row
}
type dataErrMsg struct{ err error }
type syncEvMsg struct {
	ev syncer.Event
	ch <-chan syncer.Event
}
type syncClosedMsg struct{}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tea.RequestBackgroundColor,
		m.loadData(),
		m.startSync(),
		m.spin.Tick,
	)
}

func (m Model) loadData() tea.Cmd {
	dbh, filter, w := m.deps.DB, metrics.Filter{TeamID: m.deps.TeamID, Bots: m.bots}, m.window
	return func() tea.Msg {
		rows, err := metrics.Leaderboard(dbh, filter, w)
		if err != nil {
			return dataErrMsg{err}
		}
		return dataMsg{rows: rows}
	}
}

func (m Model) startSync() tea.Cmd {
	if m.syncing {
		return nil
	}
	engine := syncer.New(m.deps.Store, m.deps.Doer, syncer.Options{
		BackfillDays: m.deps.Cfg.Sync.BackfillDays,
		PageSize:     m.deps.Cfg.Sync.PageSize,
	})
	ch := make(chan syncer.Event, 16)
	targets := m.deps.Targets
	go func() { _ = engine.SyncAll(context.Background(), targets, ch) }()
	return waitForSync(ch)
}

func waitForSync(ch <-chan syncer.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return syncClosedMsg{}
		}
		return syncEvMsg{ev: ev, ch: ch}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.theme = theme.New(msg.IsDark())
		m.spin.Style = lipgloss.NewStyle().Foreground(m.theme.Accent)
		m.board.SetTheme(&m.theme)
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.board.SetSize(msg.Width, m.contentHeight())
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.board.SetSize(m.width, m.contentHeight())
			return m, nil
		case key.Matches(msg, m.keys.CycleWindow):
			m.winIdx = (m.winIdx + 1) % len(windowPresets)
			m.window = metrics.LastDays(windowPresets[m.winIdx])
			return m, m.loadData()
		case key.Matches(msg, m.keys.Sync):
			if !m.syncing {
				m.syncing = true
				m.syncStatus = "starting sync…"
				return m, tea.Batch(m.startSync(), m.spin.Tick)
			}
			return m, nil
		}

	case dataMsg:
		m.err = nil
		m.board.SetData(msg.rows)
		return m, nil

	case dataErrMsg:
		m.err = msg.err
		return m, nil

	case syncEvMsg:
		switch ev := msg.ev.(type) {
		case syncer.RepoStarted:
			m.syncing = true
			m.syncStatus = "syncing " + ev.Repo + "…"
		case syncer.RepoPage:
			m.syncStatus = fmt.Sprintf("syncing %s… %d PRs", ev.Repo, ev.PRs)
		case syncer.RateLimited:
			m.syncStatus = "rate limited until " + ev.Until.Local().Format("15:04")
		case syncer.RepoDone:
			if ev.Err != nil {
				m.err = ev.Err
			}
		case syncer.Complete:
			m.syncing = false
			m.lastSyncDone = time.Now()
			if ev.Failed > 0 {
				m.syncStatus = fmt.Sprintf("sync finished, %d repo(s) failed", ev.Failed)
			} else {
				m.syncStatus = ""
			}
			return m, tea.Batch(waitForSync(msg.ch), m.loadData())
		}
		return m, waitForSync(msg.ch)

	case syncClosedMsg:
		m.syncing = false
		return m, m.loadData()

	case spinner.TickMsg:
		if !m.syncing {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.board, cmd = m.board.Update(msg)
	return m, cmd
}

// contentHeight is the rows available to the active page after the header,
// status bar, and help footer take theirs.
func (m Model) contentHeight() int {
	h := m.height - 2 // header + status bar
	if m.help.ShowAll {
		h -= 2
	} else {
		h -= 1
	}
	if h < 3 {
		h = 3
	}
	return h
}

func (m Model) View() tea.View {
	if m.width == 0 {
		return tea.NewView("loading…")
	}

	title := m.theme.Title.Render("Statline")
	meta := m.theme.Header.Render(
		"  " + m.deps.Team.Name + "  ·  " + m.window.Label + "  ·  sort " + m.board.SortLabel())
	header := lipgloss.JoinHorizontal(lipgloss.Center, title, meta)

	status := m.statusLine()
	helpView := m.help.View(m.keys)

	body := m.board.View()
	content := lipgloss.JoinVertical(lipgloss.Left, header, body, status, helpView)

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m Model) statusLine() string {
	style := m.theme.StatusBar
	var text string
	switch {
	case m.err != nil:
		style = m.theme.StatusError
		text = "error: " + m.err.Error()
	case m.syncing:
		text = m.spin.View() + " " + m.syncStatus
	case m.syncStatus != "":
		text = m.syncStatus
	case !m.lastSyncDone.IsZero():
		text = "✓ synced " + humanSince(m.lastSyncDone)
	default:
		text = "ready"
	}
	if len(text) > m.width-2 && m.width > 5 {
		text = text[:m.width-3] + "…"
	}
	return style.Width(m.width).Render(text)
}

func humanSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}
