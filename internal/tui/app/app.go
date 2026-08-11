// Package app hosts Statline's root Bubble Tea model: routing, global keys,
// the sync-event bridge, mouse zones, and layout composition.
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
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/syncer"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/overlays"
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

type route int

const (
	routeTeam route = iota
	routeCharts
	routeTrends
	routePerson
)

type activeOverlay int

const (
	overlayNone activeOverlay = iota
	overlayRange
	overlayTeam
)

type Model struct {
	deps  Deps
	theme theme.Theme
	keys  keys.KeyMap
	help  help.Model
	spin  spinner.Model
	z     *zone.Manager
	bots  *config.BotMatcher

	route     route
	overlay   activeOverlay
	teamStats pages.TeamStats
	charts    pages.Charts
	trends    pages.Trends
	person    pages.Person
	ranger    overlays.RangePicker
	switcher  overlays.TeamSwitcher

	winIdx int
	window metrics.Window
	custom bool

	rows        []metrics.Row
	personRepos []metrics.RepoBreakdown

	width, height int
	syncing       bool
	syncStatus    string
	active        map[string]int // repo → PRs stored so far, while syncing
	flash         string
	lastSyncDone  time.Time
	err           error              // sync/export/app-level errors
	loadErrs      [numLoadSrcs]error // per-loader errors; each success clears only its own
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
		z:      zone.New(),
		bots:   config.NewBotMatcher(deps.Cfg.ExcludeBots),
		winIdx: presetIndex(deps.Cfg.UI.Window),
		active: map[string]int{},
	}
	m.window = metrics.LastDays(windowPresets[m.winIdx])
	m.teamStats = pages.NewTeamStats(&m.theme, km, deps.Cfg.UI.Sort)
	m.teamStats.Zones = m.z
	m.charts = pages.NewCharts(&m.theme)
	m.charts.Zones = m.z
	m.trends = pages.NewTrends(&m.theme)
	m.trends.Zones = m.z
	m.person = pages.NewPerson(&m.theme)
	m.ranger = overlays.NewRangePicker(&m.theme)
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
	rows  []metrics.Row
	chart pages.ChartData
}

// loadSrc identifies which loader an error came from, so a success from one
// loader never masks a concurrent failure from another (loadData and
// loadTrends race on startup and sync completion).
type loadSrc int

const (
	srcData loadSrc = iota
	srcTrends
	srcPerson
	numLoadSrcs
)

type dataErrMsg struct {
	src loadSrc
	err error
}
type trendsMsg struct{ data metrics.TrendData }
type personMsg struct {
	login    string
	repos    []metrics.RepoBreakdown
	activity []float64
}
type syncEvMsg struct {
	ev syncer.Event
	ch <-chan syncer.Event
}
type syncClosedMsg struct{}
type exportedMsg struct {
	native bool
	text   string
	err    error
}
type clearFlashMsg struct{}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tea.RequestBackgroundColor,
		m.loadData(),
		m.loadTrends(),
		m.startSync(),
	)
}

func (m Model) loadData() tea.Cmd {
	dbh, filter, w := m.deps.DB, m.filter(), m.window
	return func() tea.Msg {
		fail := func(err error) tea.Msg { return dataErrMsg{src: srcData, err: err} }
		rows, err := metrics.TeamStats(dbh, filter, w)
		if err != nil {
			return fail(err)
		}
		chart := pages.ChartData{Rows: rows, BucketDur: metrics.BucketSize(w)}
		if chart.Buckets, err = metrics.Throughput(dbh, filter, w); err != nil {
			return fail(err)
		}
		if chart.Trend, err = metrics.CycleTrend(dbh, filter, w); err != nil {
			return fail(err)
		}
		if chart.TTFR, err = metrics.TTFRDistribution(dbh, filter, w); err != nil {
			return fail(err)
		}
		if chart.Sizes, err = metrics.SizeDistribution(dbh, filter, w); err != nil {
			return fail(err)
		}
		if chart.Matrix, err = metrics.ReviewMatrix(dbh, filter, w); err != nil {
			return fail(err)
		}
		if chart.Punch, err = metrics.PunchCard(dbh, filter, w); err != nil {
			return fail(err)
		}
		if chart.Aging, err = metrics.OpenAging(dbh, filter); err != nil {
			return fail(err)
		}
		if chart.Tiles.Cycle, chart.Tiles.TTFR, err = metrics.TeamMedians(dbh, filter, w); err != nil {
			return fail(err)
		}
		// Compare against the previous window only when the cache provably
		// reaches back that far; the coverage deepens the longer statline
		// is used, so this flips on by itself.
		prevW := metrics.PrevWindow(w)
		if floor, ok := metrics.CoverageFloor(dbh, filter.TeamID); ok && prevW.Start >= floor {
			chart.Tiles.HasPrev = true
			prevRows, err := metrics.TeamStats(dbh, filter, prevW)
			if err != nil {
				return fail(err)
			}
			for _, r := range prevRows {
				chart.Tiles.PrevOpened += r.PRsOpened
				chart.Tiles.PrevMerged += r.PRsMerged
				chart.Tiles.PrevReviews += r.ReviewsGiven
				chart.Tiles.PrevComments += r.CommentsGiven
			}
			if chart.Tiles.PrevCycle, chart.Tiles.PrevTTFR, err = metrics.TeamMedians(dbh, filter, prevW); err != nil {
				return fail(err)
			}
		}
		return dataMsg{rows: rows, chart: chart}
	}
}

// loadTrends recomputes the weekly trajectory series. Unlike loadData it is
// window-independent (always the trailing trend weeks), so it runs on
// startup, sync completion, and team switches — but never on window changes.
func (m Model) loadTrends() tea.Cmd {
	dbh, filter := m.deps.DB, m.filter()
	return func() tea.Msg {
		fail := func(err error) tea.Msg { return dataErrMsg{src: srcTrends, err: err} }
		d, err := metrics.TrendSeries(dbh, filter, metrics.TrendWeeks)
		if err != nil {
			return fail(err)
		}
		return trendsMsg{data: d}
	}
}

func (m Model) loadPerson(login string) tea.Cmd {
	dbh, filter, w := m.deps.DB, m.filter(), m.window
	return func() tea.Msg {
		fail := func(err error) tea.Msg { return dataErrMsg{src: srcPerson, err: err} }
		repos, err := metrics.PersonRepos(dbh, filter, w, login)
		if err != nil {
			return fail(err)
		}
		activity, err := metrics.PersonActivity(dbh, filter, w, login)
		if err != nil {
			return fail(err)
		}
		return personMsg{login: login, repos: repos, activity: activity}
	}
}

func (m Model) filter() metrics.Filter {
	return metrics.Filter{TeamID: m.deps.TeamID, Bots: m.bots}
}

// startSync launches a background sync unless one is already running (or the
// team is local-only). It owns the syncing state flip so every caller —
// startup, the s key, and team switches — behaves identically.
func (m *Model) startSync() tea.Cmd {
	if m.syncing || m.deps.Team.NoSync {
		return nil
	}
	m.syncing = true
	m.err = nil // a fresh attempt supersedes the last sync/app error
	m.syncStatus = "starting sync…"
	engine := syncer.New(m.deps.Store, m.deps.Doer, syncer.Options{
		BackfillDays: m.deps.Cfg.Sync.BackfillDays,
		PageSize:     m.deps.Cfg.Sync.PageSize,
		Concurrency:  m.deps.Cfg.Sync.Concurrency,
	})
	ch := make(chan syncer.Event, 16)
	targets := m.deps.Targets
	go func() { _ = engine.SyncAll(context.Background(), targets, ch) }()
	return tea.Batch(waitForSync(ch), m.spin.Tick)
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
		m.teamStats.SetTheme(&m.theme)
		m.charts.SetTheme(&m.theme)
		m.trends.SetTheme(&m.theme)
		m.person.SetTheme(&m.theme)
		m.ranger.SetTheme(&m.theme)
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		ch := m.contentHeight()
		m.teamStats.SetSize(msg.Width, ch)
		m.charts.SetSize(msg.Width, ch)
		m.trends.SetSize(msg.Width, ch)
		m.person.SetSize(msg.Width, ch)
		return m, nil

	case overlays.RangeChosenMsg:
		m.overlay = overlayNone
		m.custom = true
		m.window = metrics.Range(msg.From, msg.To)
		return m, m.reloadAll()

	case overlays.RangeCancelledMsg:
		m.overlay = overlayNone
		return m, nil

	case overlays.TeamChosenMsg:
		m.overlay = overlayNone
		if msg.Name == m.deps.Team.Name {
			return m, nil
		}
		return m.activateTeam(msg.Name)

	case overlays.TeamCancelledMsg:
		m.overlay = overlayNone
		return m, nil

	case pages.SortChangedMsg:
		if m.deps.Cfg.UI.Sort != msg.Key {
			m.deps.Cfg.UI.Sort = msg.Key
			m.persistCfg()
		}
		return m, nil

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" { // always quittable, even under a modal
			return m, tea.Quit
		}
		switch m.overlay {
		case overlayRange:
			var cmd tea.Cmd
			m.ranger, cmd = m.ranger.Update(msg)
			return m, cmd
		case overlayTeam:
			var cmd tea.Cmd
			m.switcher, cmd = m.switcher.Update(msg)
			return m, cmd
		}
		// The charts and trends pages own grid navigation and fullscreen
		// toggling; unclaimed keys fall through to the global keymap.
		if m.route == routeCharts && m.charts.HandleKey(msg.String()) {
			return m, nil
		}
		if m.route == routeTrends && m.trends.HandleKey(msg.String()) {
			return m, nil
		}
		return m.handleKey(msg)

	case tea.MouseClickMsg:
		if m.overlay != overlayNone {
			return m, nil
		}
		return m.handleClick(msg)

	case tea.MouseWheelMsg:
		if m.overlay != overlayNone {
			return m, nil
		}
		delta := 3
		if msg.Mouse().Button == tea.MouseWheelUp {
			delta = -3
		}
		switch m.route {
		case routeTeam:
			m.teamStats.Scroll(delta)
		case routePerson:
			m.person.Scroll(delta)
		case routeTrends:
			if m.trends.Fullscreen() {
				m.trends.Scroll(delta)
			}
		case routeCharts:
			if m.charts.Fullscreen() {
				m.charts.Scroll(delta)
			}
		}
		return m, nil

	case dataMsg:
		m.loadErrs[srcData] = nil
		m.rows = msg.rows
		m.teamStats.SetData(msg.rows)
		return m, m.charts.SetData(msg.chart)

	case trendsMsg:
		m.loadErrs[srcTrends] = nil
		m.trends.SetData(msg.data)
		return m, nil

	case personMsg:
		m.loadErrs[srcPerson] = nil
		m.personRepos = msg.repos
		m.person.SetData(msg.login, m.rowFor(msg.login), msg.repos, msg.activity)
		m.route = routePerson
		return m, nil

	case dataErrMsg:
		m.loadErrs[msg.src] = msg.err
		return m, nil

	case pages.ChartTickMsg:
		var cmd tea.Cmd
		m.charts, cmd = m.charts.Update(msg)
		return m, cmd

	case syncEvMsg:
		switch ev := msg.ev.(type) {
		case syncer.RepoStarted:
			wasIdle := !m.syncing
			m.syncing = true
			m.active[ev.Repo] = 0
			m.syncStatus = m.syncSummary()
			if wasIdle {
				// Startup path: Init's startSync ran on a discarded model
				// copy, so the tick chain died on arrival — restart it.
				return m, tea.Batch(waitForSync(msg.ch), m.spin.Tick)
			}
		case syncer.RepoPage:
			m.active[ev.Repo] = ev.PRs
			m.syncStatus = m.syncSummary()
		case syncer.RateLimited:
			m.syncStatus = "rate limited until " + ev.Until.Local().Format("15:04")
		case syncer.RepoDone:
			delete(m.active, ev.Repo)
			if ev.Err != nil {
				m.err = ev.Err
			}
			m.syncStatus = m.syncSummary()
		case syncer.Complete:
			m.syncing = false
			m.lastSyncDone = time.Now()
			if ev.Failed > 0 {
				m.syncStatus = fmt.Sprintf("sync finished, %d repo(s) failed", ev.Failed)
			} else {
				m.syncStatus = ""
			}
			return m, tea.Batch(waitForSync(msg.ch), m.reloadAll(), m.loadTrends())
		}
		return m, waitForSync(msg.ch)

	case syncClosedMsg:
		m.syncing = false
		return m, tea.Batch(m.reloadAll(), m.loadTrends())

	case exportedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		if !msg.native {
			// No native clipboard (e.g. SSH session): OSC52 through the
			// terminal instead.
			m.flash = "copied via terminal (OSC52)"
			return m, tea.Batch(tea.SetClipboard(msg.text), clearFlashLater())
		}
		m.flash = "copied as Markdown"
		return m, clearFlashLater()

	case clearFlashMsg:
		m.flash = ""
		return m, nil

	case spinner.TickMsg:
		// Always advance the frame; only reschedule while a sync is live so
		// the chain dies when idle (startSync restarts it).
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		if !m.syncing {
			return m, nil
		}
		return m, cmd
	}

	return m.updatePage(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m.resized()
	case key.Matches(msg, m.keys.CycleWindow):
		if m.custom {
			m.custom = false // leave custom mode, back to the current preset
		} else {
			m.winIdx = (m.winIdx + 1) % len(windowPresets)
		}
		m.window = metrics.LastDays(windowPresets[m.winIdx])
		// Presets persist; custom ranges never do, and leaving custom mode
		// back onto the unchanged preset saves nothing.
		if w := fmt.Sprintf("%dd", windowPresets[m.winIdx]); m.deps.Cfg.UI.Window != w {
			m.deps.Cfg.UI.Window = w
			m.persistCfg()
		}
		return m, m.reloadAll()
	case key.Matches(msg, m.keys.Range):
		m.ranger = overlays.NewRangePicker(&m.theme)
		m.overlay = overlayRange
		return m, nil
	case key.Matches(msg, m.keys.Team):
		names := make([]string, len(m.deps.Cfg.Teams))
		for i, t := range m.deps.Cfg.Teams {
			names[i] = t.Name
		}
		m.switcher = overlays.NewTeamSwitcher(&m.theme, names, m.deps.Team.Name)
		m.overlay = overlayTeam
		return m, nil
	case key.Matches(msg, m.keys.Sync):
		if m.deps.Team.NoSync {
			m.flash = "sync disabled for this team (no_sync)"
			return m, clearFlashLater()
		}
		return m, m.startSync()
	case key.Matches(msg, m.keys.Tab):
		switch m.route {
		case routeTeam:
			m.route = routeCharts
		case routeCharts:
			m.route = routeTrends
		case routeTrends:
			m.route = routeTeam
		default: // person drill-down cycles into charts, as before
			m.route = routeCharts
		}
		return m, nil
	case key.Matches(msg, m.keys.TeamStats):
		m.route = routeTeam
		return m, nil
	case key.Matches(msg, m.keys.Charts):
		m.route = routeCharts
		return m, nil
	case key.Matches(msg, m.keys.Trends):
		m.route = routeTrends
		return m, nil
	case key.Matches(msg, m.keys.Drill):
		if m.route == routeTeam {
			if login := m.teamStats.SelectedLogin(); login != "" {
				return m, m.loadPerson(login)
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.Back):
		if m.route == routePerson {
			m.route = routeTeam
		}
		return m, nil
	case key.Matches(msg, m.keys.Export):
		return m, m.exportCurrent()
	}
	return m.updatePage(msg)
}

func (m Model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Mouse().Button != tea.MouseLeft {
		return m, nil
	}
	if z := m.z.Get("tab:team"); z.InBounds(msg) {
		m.route = routeTeam
		return m, nil
	}
	if z := m.z.Get("tab:charts"); z.InBounds(msg) {
		m.route = routeCharts
		return m, nil
	}
	if z := m.z.Get("tab:trends"); z.InBounds(msg) {
		m.route = routeTrends
		return m, nil
	}
	if m.route == routeTeam {
		for _, r := range m.rows {
			if z := m.z.Get("row:" + r.Login); z.InBounds(msg) {
				return m, m.loadPerson(r.Login)
			}
		}
	}
	if m.route == routeCharts && !m.charts.Fullscreen() {
		for _, k := range m.charts.CardKeys() {
			if z := m.z.Get("card:" + k); z.InBounds(msg) {
				m.charts.ClickCard(k)
				return m, nil
			}
		}
	}
	if m.route == routeTrends && !m.trends.Fullscreen() {
		for _, k := range m.trends.CardKeys() {
			if z := m.z.Get("trend:" + k); z.InBounds(msg) {
				m.trends.ClickCard(k)
				return m, nil
			}
		}
	}
	return m, nil
}

func (m Model) updatePage(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.route {
	case routeTeam:
		m.teamStats, cmd = m.teamStats.Update(msg)
	case routePerson:
		m.person, cmd = m.person.Update(msg)
	}
	return m, cmd
}

func (m Model) resized() (tea.Model, tea.Cmd) {
	ch := m.contentHeight()
	m.teamStats.SetSize(m.width, ch)
	m.charts.SetSize(m.width, ch)
	m.trends.SetSize(m.width, ch)
	m.person.SetSize(m.width, ch)
	return m, nil
}

// reloadAll refreshes the data behind whichever views are live. Trends is
// deliberately not included: its weekly series ignores the time window, so
// only sync completion and team switches issue loadTrends.
func (m Model) reloadAll() tea.Cmd {
	cmds := []tea.Cmd{m.loadData()}
	if m.route == routePerson && m.person.Login != "" {
		cmds = append(cmds, m.loadPerson(m.person.Login))
	}
	return tea.Batch(cmds...)
}

// activateTeam switches the live team profile: re-mirrors config into the
// DB, rebuilds sync targets, and refreshes data plus a background sync.
func (m Model) activateTeam(name string) (tea.Model, tea.Cmd) {
	team, ok := m.deps.Cfg.TeamByName(name)
	if !ok {
		m.err = fmt.Errorf("team %q not in config", name)
		return m, nil
	}
	teamID, repoIDs, err := m.deps.Store.MirrorTeam(team)
	if err != nil {
		m.err = err
		return m, nil
	}
	m.err = nil
	m.deps.Team = team
	m.deps.TeamID = teamID
	m.deps.Targets = m.deps.Targets[:0]
	for _, r := range team.Repos {
		m.deps.Targets = append(m.deps.Targets,
			syncer.Target{Owner: r.Owner, Name: r.Name, RepoID: repoIDs[r.String()]})
	}
	m.route = routeTeam
	m.rows = nil
	m.teamStats.SetData(nil)
	m.trends.Reset()
	cmds := []tea.Cmd{m.loadData(), m.loadTrends(), m.startSync()}
	// After startSync: it clears m.err, which would swallow a save failure.
	if m.deps.Cfg.DefaultTeam != name {
		m.deps.Cfg.DefaultTeam = name
		m.persistCfg()
	}
	return m, tea.Batch(cmds...)
}

// persistCfg writes the in-memory config back to disk after an in-app state
// change (team switch, window preset, sort column). Failure surfaces in the
// status bar; the session keeps running on the in-memory value either way.
func (m *Model) persistCfg() {
	if err := config.Save(m.deps.Cfg); err != nil {
		m.err = fmt.Errorf("saving config: %w", err)
	}
}

func (m Model) rowFor(login string) metrics.Row {
	for _, r := range m.rows {
		if r.Login == login {
			return r
		}
	}
	return metrics.Row{Login: login, SizeP50: -1}
}

func (m Model) exportCurrent() tea.Cmd {
	var text string
	switch m.route {
	case routePerson:
		text = export.Person(m.person.Login, m.window, m.rowFor(m.person.Login), m.personRepos)
	case routeCharts:
		if title, headers, rows, ok := m.charts.ExportTable(); ok {
			text = export.Table(title+" — "+m.window.Label, headers, rows)
			break
		}
		text = export.TeamStats(m.deps.Team.Name, m.window, m.rows)
	case routeTrends:
		if title, headers, rows, ok := m.trends.ExportTable(); ok {
			text = export.Table(title+" — weekly trend", headers, rows)
			break
		}
		d, risers, fallers := m.trends.ExportData()
		text = export.Trends(m.deps.Team.Name, d, risers, fallers)
	default:
		text = export.TeamStats(m.deps.Team.Name, m.window, m.rows)
	}
	return func() tea.Msg {
		native, err := export.ToClipboard(text)
		return exportedMsg{native: native, text: text, err: err}
	}
}

func clearFlashLater() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })
}

// contentHeight is the rows available to the active page after the header,
// status bar, and help footer take theirs.
func (m Model) contentHeight() int {
	h := m.height - 2 // header + status bar
	if m.help.ShowAll {
		h -= 3
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

	header := m.headerLine()
	status := m.statusLine()
	helpView := m.help.View(m.keys)

	var body string
	switch m.route {
	case routeCharts:
		body = m.charts.View()
	case routeTrends:
		body = m.trends.View()
	case routePerson:
		body = m.person.View()
	default:
		body = m.teamStats.View()
	}
	switch m.overlay {
	case overlayRange:
		body = lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center,
			m.ranger.View())
	case overlayTeam:
		body = lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center,
			m.switcher.View())
	}

	content := lipgloss.JoinVertical(lipgloss.Left, header, body, status, helpView)
	v := tea.NewView(m.z.Scan(content))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) headerLine() string {
	title := m.theme.Title.Render("Statline")

	tab := func(id, label string, active bool) string {
		style := m.theme.Header
		if active {
			style = lipgloss.NewStyle().Bold(true).Foreground(m.theme.Accent)
		}
		return m.z.Mark(id, style.Render(label))
	}
	// The person drill-down keeps the Team tab lit — it's a detail view of
	// that tab.
	sep := m.theme.Header.Render(" │ ")
	tabs := "  " + tab("tab:team", "1 Team", m.route == routeTeam || m.route == routePerson) +
		sep + tab("tab:charts", "2 Charts", m.route == routeCharts) +
		sep + tab("tab:trends", "3 Trends", m.route == routeTrends)

	meta := m.theme.Header.Render("  ·  " + m.deps.Team.Name + "  ·  " + m.window.Label +
		"  ·  sort " + m.teamStats.SortLabel())
	return lipgloss.JoinHorizontal(lipgloss.Center, title, tabs, meta)
}

// firstError returns the app-level error or, failing that, the first live
// loader error, so any failure surfaces even while other loads succeed.
func (m Model) firstError() error {
	if m.err != nil {
		return m.err
	}
	for _, e := range m.loadErrs {
		if e != nil {
			return e
		}
	}
	return nil
}

func (m Model) statusLine() string {
	style := m.theme.StatusBar
	var text string
	switch {
	case m.flash != "":
		text = "✓ " + m.flash
	case m.firstError() != nil:
		style = m.theme.StatusError
		text = "error: " + m.firstError().Error()
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

// syncSummary condenses concurrent repo walks into one status line.
func (m Model) syncSummary() string {
	if len(m.active) == 0 {
		return "finishing sync…"
	}
	total := 0
	for _, n := range m.active {
		total += n
	}
	return fmt.Sprintf("syncing %d repo(s) · %d PRs", len(m.active), total)
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
