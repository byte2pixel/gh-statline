// Package app hosts Statline's root Bubble Tea model: routing, global keys,
// the sync-event bridge, mouse zones, and layout composition.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/cli/go-gh/v2/pkg/browser"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/doctor"
	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/syncer"
	"github.com/byte2pixel/gh-statline/internal/text"
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
	// Now is the clock behind the time windows and open-PR aging; nil
	// means the wall clock. Tests pin it.
	Now func() time.Time
	// Clipboard receives an export; nil means the system clipboard
	// (export.ToClipboard). Tests capture the text instead.
	Clipboard func(text string) (native bool, err error)
	// Browser opens a URL; nil means gh's own launcher resolution, which
	// reads GH_BROWSER, then gh's browser setting, then BROWSER, then asks
	// the OS. Tests capture the URL instead.
	Browser func(url string) error
}

// openInBrowser is the production Browser. The launcher's own output is
// discarded: a chatty one would write over the frame.
func openInBrowser(url string) error {
	return browser.New("", io.Discard, io.Discard).Browse(url)
}

var windowPresets = []int{7, 14, 30, 90}

type route int

const (
	routeTeam route = iota
	routeCharts
	routeTrends
	routePerson
	routeSyncStatus
	numRoutes
)

type activeOverlay int

const (
	overlayNone activeOverlay = iota
	overlayRange
	overlayTeam
	overlayRepos
)

type Model struct {
	deps  Deps
	theme theme.Theme
	keys  keys.KeyMap
	help  help.Model
	spin  spinner.Model
	z     *zone.Manager
	bots  *config.BotMatcher

	nav     router
	overlay activeOverlay
	// The concrete pages carry the typed data wiring; the registry below
	// serves every uniform concern (theme, size, keys, update, view).
	teamStats *pages.TeamStats
	charts    *pages.Charts
	trends    *pages.Trends
	person    *pages.Person
	// syncHealth is both the sync-status page and the source of the
	// warning badge's count, so the report has exactly one owner.
	syncHealth *pages.SyncStatus
	pages      [numRoutes]Page // every routed page, indexed by route
	ranger     overlays.RangePicker
	switcher   overlays.TeamSwitcher
	picker     overlays.RepoPicker

	winIdx int
	window metrics.Window
	custom bool
	// repoIDs narrows every number to these repos; nil means all of the
	// team's. One-shot per session, like a custom range: the ids are
	// cache-local and belong to the active team, so a team switch drops
	// them and nothing writes them to config.
	repoIDs []int64

	width, height int
	// themeLocked records that ui.theme named a palette, so a terminal
	// that answers the background query cannot overrule the file.
	themeLocked  bool
	syncing      bool
	syncCancel   context.CancelFunc  // stops the running sync; nil when idle
	syncCh       <-chan syncer.Event // identifies the current run's event stream
	quitting     bool                // quit requested; leave once the sync winds down
	syncStatus   string
	active       map[string]int // repo → PRs stored so far, while syncing
	flash        string
	lastSyncDone time.Time
	err          error              // sync/export/app-level errors
	loadErrs     [numLoadSrcs]error // per-loader errors; each success clears only its own
}

func New(deps Deps) Model {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Clipboard == nil {
		deps.Clipboard = export.ToClipboard
	}
	if deps.Browser == nil {
		deps.Browser = openInBrowser
	}
	// ui.theme pins the palette. Without it, the app asks the terminal for
	// its background and assumes dark until the answer lands.
	dark, themeLocked := deps.Cfg.UI.ThemeMode()
	th := theme.New(dark)
	km := keys.Default()

	m := Model{
		deps:        deps,
		theme:       th,
		themeLocked: themeLocked,
		keys:        km,
		nav:         newRouter(km),
		help:        help.New(),
		spin:        spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		z:           zone.New(),
		bots:        config.NewBotMatcher(deps.Cfg.ExcludeBots),
		winIdx:      presetIndex(deps.Cfg.UI.Window),
		active:      map[string]int{},
	}
	m.window = metrics.LastDays(windowPresets[m.winIdx], m.deps.Now())
	m.teamStats = pages.NewTeamStats(&m.theme, km, deps.Cfg.UI.Sort)
	m.teamStats.Zones = m.z
	m.charts = pages.NewCharts(&m.theme, km)
	m.charts.Zones = m.z
	m.trends = pages.NewTrends(&m.theme, km)
	m.trends.Zones = m.z
	m.person = pages.NewPerson(&m.theme)
	m.syncHealth = pages.NewSyncStatus(&m.theme, km)
	m.pages = [numRoutes]Page{
		routeTeam:       m.teamStats,
		routeCharts:     m.charts,
		routeTrends:     m.trends,
		routePerson:     m.person,
		routeSyncStatus: m.syncHealth,
	}
	m.setNoSync(deps.Team.NoSync)
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
type dataMsg struct{ d metrics.Dashboard }

// loadSrc identifies which loader an error came from, so a success from one
// loader never masks a concurrent failure from another (loadData and
// loadTrends race on startup and sync completion).
type loadSrc int

const (
	srcData loadSrc = iota
	srcTrends
	srcPerson
	srcSyncHealth
	numLoadSrcs
)

type dataErrMsg struct {
	src loadSrc
	err error
}
type trendsMsg struct{ data metrics.TrendData }
type syncHealthMsg struct{ rep doctor.Report }
type personMsg struct {
	login string
	data  metrics.PersonData
}
type syncEvMsg struct {
	ev syncer.Event
	ch <-chan syncer.Event
}
type syncClosedMsg struct{ ch <-chan syncer.Event }
type startSyncMsg struct{}
type quitTimeoutMsg struct{}
type exportedMsg struct {
	native bool
	text   string
	err    error
}
type openedMsg struct {
	label string // repo#number, for the flash or the error
	err   error
}
type clearFlashMsg struct{}

func (m Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, 4)
	if !m.themeLocked {
		// A pinned theme has nothing to learn from the answer, and the
		// terminals that never send one are exactly who pinned it.
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	cmds = append(cmds,
		m.loadData(),
		m.loadTrends(),
		m.loadSyncHealth(),
		// Init has a value receiver, so calling startSync here would flip
		// m.syncing on a discarded copy and leave a window where the s key
		// launches a second engine. Route through Update instead, which runs
		// on the live model.
		func() tea.Msg { return startSyncMsg{} },
	)
	return tea.Batch(cmds...)
}

func (m Model) loadData() tea.Cmd {
	dbh, filter, w, now := m.deps.DB, m.filter(), m.window, m.deps.Now()
	return func() tea.Msg {
		d, err := metrics.LoadDashboard(dbh, filter, w, now)
		if err != nil {
			return dataErrMsg{src: srcData, err: err}
		}
		return dataMsg{d: d}
	}
}

// loadTrends recomputes the weekly trajectory series. Unlike loadData it is
// window-independent (always the trailing trend weeks), so it runs on
// startup, sync completion, and team switches — but never on window changes.
func (m Model) loadTrends() tea.Cmd {
	dbh, filter, now := m.deps.DB, m.filter(), m.deps.Now()
	return func() tea.Msg {
		fail := func(err error) tea.Msg { return dataErrMsg{src: srcTrends, err: err} }
		d, err := metrics.TrendSeries(dbh, filter, metrics.TrendWeeks, now)
		if err != nil {
			return fail(err)
		}
		return trendsMsg{data: d}
	}
}

func (m Model) loadPerson(login string) tea.Cmd {
	dbh, filter, w := m.deps.DB, m.filter(), m.window
	return func() tea.Msg {
		d, err := metrics.LoadPerson(dbh, filter, w, login)
		if err != nil {
			return dataErrMsg{src: srcPerson, err: err}
		}
		return personMsg{login: login, data: d}
	}
}

// loadSyncHealth re-reads the per-repo sync bookkeeping. It runs on
// startup and after every sync, not on window changes: the cache's health
// has nothing to do with which period is on screen. Reading it from the
// database rather than from this session's sync events is the point — a
// repo that has been failing since yesterday is a warning the moment the
// app opens, before any sync has run.
func (m Model) loadSyncHealth() tea.Cmd {
	store, team, teamID, now := m.deps.Store, m.deps.Team, m.deps.TeamID, m.deps.Now()
	return func() tea.Msg {
		rep, err := doctor.Load(store, team, teamID, now)
		if err != nil {
			return dataErrMsg{src: srcSyncHealth, err: err}
		}
		return syncHealthMsg{rep: rep}
	}
}

// filter is the one scope every loader reads: the team, the repo filter,
// and the bot policy. The person drill-down and the exports go through it
// too, so a repo filter can never show on one view and not another.
func (m Model) filter() metrics.Filter {
	return metrics.Filter{TeamID: m.deps.TeamID, RepoIDs: m.repoIDs, Bots: m.bots}
}

// startSync launches a background sync unless one is already running, the
// app is quitting, or the team is local-only. It owns the syncing state flip
// so every caller — startup, the s key, and team switches — behaves
// identically.
func (m *Model) startSync() tea.Cmd {
	if m.syncing || m.quitting || m.deps.Team.NoSync {
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
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan syncer.Event, 16)
	m.syncCancel = cancel
	m.syncCh = ch
	targets := m.deps.Targets
	go func() { _ = engine.SyncAll(ctx, targets, ch) }()
	return tea.Batch(waitForSync(ch), m.spin.Tick)
}

// releaseSync forgets the current run. Cancel is idempotent, so calling it
// after a natural Complete only frees the context.
func (m *Model) releaseSync() {
	if m.syncCancel != nil {
		m.syncCancel()
		m.syncCancel = nil
	}
	m.syncCh = nil
	m.syncing = false
	m.active = map[string]int{}
}

// requestQuit cancels a running sync and holds the quit until the engine's
// event stream closes, which SyncAll guarantees happens after its last
// database write, so the deferred DB close in cmd/root never races a write.
// A second press, or the fallback timer, quits without waiting.
func (m *Model) requestQuit() tea.Cmd {
	if m.quitting || !m.syncing {
		// Also blocks a not-yet-processed startSyncMsg from launching a sync
		// after the quit is already underway.
		m.quitting = true
		return tea.Quit
	}
	m.quitting = true
	if m.syncCancel != nil {
		m.syncCancel()
	}
	m.syncStatus = "cancelling sync…"
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return quitTimeoutMsg{} })
}

func waitForSync(ch <-chan syncer.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return syncClosedMsg{ch: ch}
		}
		return syncEvMsg{ev: ev, ch: ch}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		if m.themeLocked { // ui.theme outranks whatever the terminal reports
			return m, nil
		}
		m.theme = theme.New(msg.IsDark())
		m.spin.Style = lipgloss.NewStyle().Foreground(m.theme.Accent)
		for _, p := range m.pages {
			p.SetTheme(&m.theme)
		}
		m.ranger.SetTheme(&m.theme)
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width) // the full help drops columns instead of wrapping
		m.layoutPages()
		// The picker scrolls inside the same area the pages get, so it
		// follows the resize too; harmless while it is closed.
		m.picker.SetHeight(m.contentHeight())
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

	case overlays.TeamDeleteMsg:
		return m.deleteTeam(msg.Name)

	case overlays.ReposChosenMsg:
		m.overlay = overlayNone
		m.repoIDs = msg.IDs
		// Trends ignore the window but not the repo filter, and reloadAll
		// leaves them out on purpose, so they are asked for here by name.
		return m, tea.Batch(m.reloadAll(), m.loadTrends())

	case overlays.ReposCancelledMsg:
		m.overlay = overlayNone
		return m, nil

	case pages.SortChangedMsg:
		if m.deps.Cfg.UI.Sort != msg.Key {
			m.deps.Cfg.UI.Sort = msg.Key
			m.persistCfg()
		}
		return m, nil

	case pages.MemberChosenMsg:
		return m, m.loadPerson(msg.Login)

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" { // always quittable, even under a modal
			return m, m.requestQuit()
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
		case overlayRepos:
			var cmd tea.Cmd
			m.picker, cmd = m.picker.Update(msg)
			return m, cmd
		}
		// The active page gets first refusal (grid navigation, fullscreen
		// toggling); unclaimed keys fall through to the global keymap.
		if m.page().HandleKey(msg) {
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
		m.page().Scroll(delta)
		return m, nil

	case dataMsg:
		m.loadErrs[srcData] = nil
		m.teamStats.SetData(msg.d.Rows)
		return m, m.charts.SetData(msg.d)

	case trendsMsg:
		m.loadErrs[srcTrends] = nil
		m.trends.SetData(msg.data)
		return m, nil

	case syncHealthMsg:
		m.loadErrs[srcSyncHealth] = nil
		m.syncHealth.SetData(msg.rep)
		return m, nil

	case personMsg:
		m.loadErrs[srcPerson] = nil
		m.person.SetData(msg.login, m.teamStats.RowFor(msg.login), msg.data.Repos, msg.data.Activity)
		m.nav.cur = routePerson
		return m, nil

	case dataErrMsg:
		m.loadErrs[msg.src] = msg.err
		return m, nil

	case startSyncMsg:
		return m, m.startSync()

	case quitTimeoutMsg:
		if m.quitting { // the sync did not wind down in time; leave anyway
			return m, tea.Quit
		}
		return m, nil

	case pages.ChartTickMsg:
		// Straight to the charts page, not the active one: the bar springs
		// keep settling while another tab is in front.
		return m, m.charts.Update(msg)

	case syncEvMsg:
		if msg.ch != m.syncCh {
			// A cancelled run's stream: drop the event and stop pumping it.
			// The engine exits on its own now that its context is done.
			return m, nil
		}
		if m.quitting {
			// Keep draining so the engine can finish, but the only event
			// that matters now is the end of the run.
			if _, done := msg.ev.(syncer.Complete); done {
				return m, tea.Quit
			}
			return m, waitForSync(msg.ch)
		}
		switch ev := msg.ev.(type) {
		case syncer.RepoStarted:
			m.active[ev.Repo] = 0
			m.syncStatus = m.syncSummary()
		case syncer.RepoPage:
			m.active[ev.Repo] = ev.PRs
			m.syncStatus = m.syncSummary()
		case syncer.RateLimited:
			// Local time with the zone attached. Everything else statline
			// shows is UTC, so an unlabelled wall clock is a coin flip.
			m.syncStatus = "rate limited until " + ev.Until.Local().Format("15:04 MST")
		case syncer.RepoDone:
			// The error is not raised here: the engine has already recorded
			// it against the repo, and the sync-status view shows it whole
			// and per repo instead of shearing one of them into the status
			// bar and hiding the freshness line behind it. A failure that
			// could not even be recorded surfaces through the health load,
			// which reads the same broken database.
			delete(m.active, ev.Repo)
			m.syncStatus = m.syncSummary()
		case syncer.Complete:
			m.releaseSync()
			m.lastSyncDone = m.deps.Now()
			// Failures are not announced here. This line is cleared on the
			// next sync, so it could only ever say "something broke during
			// the run you just watched"; the badge below reads the recorded
			// state instead and survives restarts.
			m.syncStatus = ""
			// No waitForSync here: the channel close that follows Complete
			// carries no news, and pumping it would reload everything twice.
			return m, tea.Batch(m.reloadAll(), m.loadTrends(), m.loadSyncHealth())
		}
		return m, waitForSync(msg.ch)

	case syncClosedMsg:
		if msg.ch != m.syncCh {
			return m, nil
		}
		if m.quitting {
			return m, tea.Quit
		}
		// Only reachable when Complete was dropped (a cancelled run whose
		// stream we still own); treat the close itself as the end.
		m.releaseSync()
		return m, tea.Batch(m.reloadAll(), m.loadTrends(), m.loadSyncHealth())

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

	case openedMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("opening %s: %w", msg.label, msg.err)
			return m, nil
		}
		m.err = nil
		m.flash = "opened " + msg.label + " in your browser"
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
	if m.nav.jumpKey(msg) { // numeric tab shortcuts
		return m, nil
	}
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, m.requestQuit()
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.layoutPages()
		return m, nil
	case key.Matches(msg, m.keys.CycleWindow):
		if m.custom {
			m.custom = false // leave custom mode, back to the current preset
		} else {
			m.winIdx = (m.winIdx + 1) % len(windowPresets)
		}
		m.window = metrics.LastDays(windowPresets[m.winIdx], m.deps.Now())
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
	case key.Matches(msg, m.keys.Repos):
		choices := m.repoChoices()
		if len(choices) == 0 {
			m.flash = "no repos configured for this team"
			return m, clearFlashLater()
		}
		m.picker = overlays.NewRepoPicker(&m.theme, choices, m.repoIDs)
		m.picker.SetHeight(m.contentHeight())
		m.overlay = overlayRepos
		return m, nil
	case key.Matches(msg, m.keys.Sync):
		if m.deps.Team.NoSync {
			m.flash = "sync disabled for this team (no_sync)"
			return m, clearFlashLater()
		}
		return m, m.startSync()
	case key.Matches(msg, m.keys.Tab):
		m.nav.cycle()
		return m, nil
	case key.Matches(msg, m.keys.SyncStatus):
		// Toggles, the way t closes the team switcher: the key that opened
		// a non-tab view is the obvious way back out of it.
		if m.nav.cur == routeSyncStatus {
			m.nav.back()
		} else {
			m.nav.enter(routeSyncStatus)
		}
		return m, nil
	case key.Matches(msg, m.keys.Drill):
		if m.nav.cur == routeTeam {
			if login := m.teamStats.SelectedLogin(); login != "" {
				return m, m.loadPerson(login)
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.Back):
		switch m.nav.cur {
		case routePerson:
			m.nav.cur = routeTeam
		case routeSyncStatus:
			m.nav.back()
		}
		return m, nil
	case key.Matches(msg, m.keys.Export):
		return m, m.exportCurrent()
	case key.Matches(msg, m.keys.Open):
		return m.openSelected()
	}
	return m.updatePage(msg)
}

// openSelected hands the highlighted pull request to the browser. The
// open-PR card is the one view that names a PR today, so anywhere else the
// key says where to find one instead of doing nothing.
func (m Model) openSelected() (tea.Model, tea.Cmd) {
	pr, ok := m.charts.SelectedPR()
	if m.nav.cur != routeCharts || !ok {
		m.flash = "o opens a PR from the Open PRs card on the charts tab"
		return m, clearFlashLater()
	}
	// Repo is owner/name straight from the cache, which mirrors the config;
	// sanitized anyway before it can reach the status bar.
	label := text.Sanitize(fmt.Sprintf("%s#%d", pr.Repo, pr.Number))
	url := fmt.Sprintf("https://%s/%s/pull/%d", gh.Host(), pr.Repo, pr.Number)
	open := m.deps.Browser
	return m, func() tea.Msg { return openedMsg{label: label, err: open(url)} }
}

func (m Model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Mouse().Button != tea.MouseLeft {
		return m, nil
	}
	if m.nav.clickTab(msg, m.z) {
		return m, nil
	}
	// Only the active page sees the click, so a stale zone left over from
	// another route's render can never match.
	return m, m.page().HandleClick(msg)
}

// page is the active route's view.
func (m Model) page() Page { return m.pages[m.nav.cur] }

func (m Model) updatePage(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, m.page().Update(msg)
}

// layoutPages hands every page the current content area. Both resize
// triggers — the terminal's WindowSizeMsg and the help toggle changing the
// footer height — land here, so the two paths can never drift.
func (m *Model) layoutPages() {
	ch := m.contentHeight()
	for _, p := range m.pages {
		p.SetSize(m.width, ch)
	}
}

// setNoSync tells the pages whether the active team is local-only. It
// follows the team rather than a data load, so the empty state is right
// before the first data lands, and right again after a team switch.
func (m *Model) setNoSync(v bool) {
	m.teamStats.SetNoSync(v)
	m.charts.SetNoSync(v)
	m.trends.SetNoSync(v)
}

// reloadAll refreshes the data behind whichever views are live. Trends is
// deliberately not included: its weekly series ignores the time window, so
// only sync completion and team switches issue loadTrends.
func (m Model) reloadAll() tea.Cmd {
	cmds := []tea.Cmd{m.loadData()}
	if m.nav.cur == routePerson && m.person.Login != "" {
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
	// Cancellation below is asynchronous, so the old team's sync may still be
	// ranging the old slice for a moment; build a fresh one. Reusing the
	// backing array would rewrite targets underneath it and store PRs against
	// the wrong repo.
	targets := make([]syncer.Target, 0, len(team.Repos))
	for _, r := range team.Repos {
		targets = append(targets,
			syncer.Target{Owner: r.Owner, Name: r.Name, RepoID: repoIDs[r.String()]})
	}
	m.deps.Targets = targets
	m.repoIDs = nil // the ids named the old team's repos
	m.nav.home()
	m.setNoSync(team.NoSync)
	m.teamStats.SetData(nil)
	m.trends.Reset()
	// Cancel the old team's sync and forget its stream, so its late events
	// can't flip state under the run startSync begins for the new team.
	m.releaseSync()
	cmds := []tea.Cmd{m.loadData(), m.loadTrends(), m.loadSyncHealth(), m.startSync()}
	// After startSync: it clears m.err, which would swallow a save failure.
	if m.deps.Cfg.DefaultTeam != name {
		m.deps.Cfg.DefaultTeam = name
		m.persistCfg()
	}
	return m, tea.Batch(cmds...)
}

// deleteTeam removes a team profile from the DB mirror and the config. The
// switcher blocks deleting the last team; the guard here is a backstop. The
// overlay stays open so the user can keep managing teams.
func (m Model) deleteTeam(name string) (tea.Model, tea.Cmd) {
	idx := -1
	for i, t := range m.deps.Cfg.Teams {
		if t.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 || len(m.deps.Cfg.Teams) <= 1 {
		return m, nil
	}
	// DB first: config is the source of truth, so a failed config save after
	// this leaves nothing broken — MirrorTeam re-creates rows on activation.
	if err := m.deps.Store.DeleteTeam(name); err != nil {
		m.err = err
		return m, nil
	}
	m.err = nil
	m.deps.Cfg.Teams = append(m.deps.Cfg.Teams[:idx], m.deps.Cfg.Teams[idx+1:]...)

	// Re-point the default before saving so the config stays valid. The
	// active team can differ from the default under a --team override.
	active := m.deps.Team.Name
	if active == name {
		active = m.deps.Cfg.Teams[0].Name
	}
	if m.deps.Cfg.DefaultTeam == name {
		m.deps.Cfg.DefaultTeam = active
	}

	names := make([]string, len(m.deps.Cfg.Teams))
	for i, t := range m.deps.Cfg.Teams {
		names[i] = t.Name
	}
	m.switcher = overlays.NewTeamSwitcher(&m.theme, names, active)
	if m.deps.Team.Name == name {
		// Save after activating: activateTeam clears m.err on success,
		// which would swallow a save failure. DefaultTeam already points
		// at the new team, so activateTeam skips its own redundant save.
		model, cmd := m.activateTeam(active)
		if m2, ok := model.(Model); ok {
			m2.persistCfg()
			return m2, cmd
		}
		return model, cmd
	}
	m.persistCfg()
	return m, nil
}

// persistCfg writes the in-memory config back to disk after an in-app state
// change (team switch, window preset, sort column). Failure surfaces in the
// status bar; the session keeps running on the in-memory value either way.
func (m *Model) persistCfg() {
	if err := config.Save(m.deps.Cfg); err != nil {
		m.err = fmt.Errorf("saving config: %w", err)
	}
}

func (m Model) exportCurrent() tea.Cmd {
	md := m.page().Export(m.exportScope(), m.window)
	clip := m.deps.Clipboard
	return func() tea.Msg {
		native, err := clip(md)
		return exportedMsg{native: native, text: md, err: err}
	}
}

// repoChoices lists the active team's repos for the picker. They come from
// the sync targets, which are the config list with cache ids attached and
// are rebuilt on every team switch, so the picker never offers a repo the
// filter could not name.
func (m Model) repoChoices() []overlays.RepoChoice {
	out := make([]overlays.RepoChoice, 0, len(m.deps.Targets))
	for _, t := range m.deps.Targets {
		out = append(out, overlays.RepoChoice{ID: t.RepoID, Name: t.Owner + "/" + t.Name})
	}
	return out
}

// filteredRepos names the repos the filter keeps, in config order; nil when
// every repo is in scope.
func (m Model) filteredRepos() []string {
	if len(m.repoIDs) == 0 {
		return nil
	}
	keep := make(map[int64]bool, len(m.repoIDs))
	for _, id := range m.repoIDs {
		keep[id] = true
	}
	var names []string
	for _, t := range m.deps.Targets {
		if keep[t.RepoID] {
			names = append(names, text.Sanitize(t.Owner+"/"+t.Name))
		}
	}
	return names
}

// scopeLabel is the header's repo-filter segment: the repo when one is in
// scope, a count when several are, empty when the filter is off.
func (m Model) scopeLabel() string {
	names := m.filteredRepos()
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return fmt.Sprintf("%d/%d repos", len(names), len(m.deps.Targets))
	}
}

// exportScope is the heading the exports carry in place of the bare team
// name while a repo filter is on. A pasted table that covers one repo has
// to say so, or it reads as the whole team's numbers.
func (m Model) exportScope() string {
	names := m.filteredRepos()
	if len(names) == 0 {
		return m.deps.Team.Name
	}
	return m.deps.Team.Name + " · " + strings.Join(names, ", ")
}

func clearFlashLater() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{} })
}

// contentHeight is the rows available to the active page after the header,
// status bar, and help footer take theirs. It measures the footer instead
// of guessing at it. The full help renders one row per binding in its
// tallest column, six today, and the constant this replaced said three, so
// every ? pushed the frame three rows past the bottom of the terminal.
func (m Model) contentHeight() int {
	h := m.height - 2 - lipgloss.Height(m.help.View(m.keys)) // header + status bar + footer
	return max(h, 3)
}

func (m Model) View() tea.View {
	if m.width == 0 {
		return tea.NewView("loading…")
	}

	header := m.headerLine()
	status := m.statusLine()
	helpView := m.help.View(m.keys)

	body := m.page().View()
	switch m.overlay {
	case overlayRange:
		body = lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center,
			m.ranger.View())
	case overlayTeam:
		body = lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center,
			m.switcher.View())
	case overlayRepos:
		body = lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center,
			m.picker.View())
	}

	content := lipgloss.JoinVertical(lipgloss.Left, header, body, status, helpView)
	v := tea.NewView(m.z.Scan(content))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) headerLine() string {
	title := m.theme.Title.Render("Statline")
	tabs := "  " + m.nav.renderTabs(&m.theme, m.z)
	meta := "  ·  " + text.Sanitize(m.deps.Team.Name)
	if scope := m.scopeLabel(); scope != "" {
		meta += "  ·  " + scope
	}
	meta += "  ·  " + m.window.Label + "  ·  sort " + m.teamStats.SortLabel()
	return lipgloss.JoinHorizontal(lipgloss.Center, title, tabs, m.theme.Header.Render(meta))
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

// failingRepos is the badge count. New always builds the page, but the
// status-bar tests construct a Model literal with no pages at all, and a
// status line that panics on one is a worse bargain than a nil check.
func (m Model) failingRepos() int {
	if m.syncHealth == nil {
		return 0
	}
	return m.syncHealth.Failing()
}

func (m Model) statusLine() string {
	style := m.theme.StatusBar
	var line string
	switch {
	case m.flash != "":
		line = "✓ " + m.flash
	case m.firstError() != nil:
		// Errors wrap API and config content; strip anything that could
		// smuggle an escape sequence onto the status bar.
		style = m.theme.StatusError
		line = "error: " + text.Sanitize(m.firstError().Error())
	case m.syncing:
		line = m.spin.View() + " " + m.syncStatus
	case m.syncStatus != "":
		line = m.syncStatus
	case m.failingRepos() > 0:
		// Above the freshness line and below anything transient: a repo
		// that stopped syncing outlives the session that noticed, and
		// "synced 4m ago" is exactly the reassurance it must not give.
		line = fmt.Sprintf("⚠ %d repo(s) failing · press S", m.failingRepos())
	case !m.lastSyncDone.IsZero():
		line = "✓ synced " + humanSince(m.deps.Now(), m.lastSyncDone)
	default:
		line = "ready"
	}
	// Truncate by display cells: byte slicing here used to shear the ✓ (and
	// any wide character) into mojibake on narrow windows.
	if m.width > 5 {
		line = text.Truncate(line, m.width-2)
	}
	return style.Width(m.width).Render(line)
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

// humanSince is how long ago a moment in this session was, in the coarse
// vocabulary the status bar has room for. It takes now rather than reading
// the wall clock, so the freshness line follows Deps.Now like everything
// else the app renders and a pinned clock produces a pinned age.
func humanSince(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}
