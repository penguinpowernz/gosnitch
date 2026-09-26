package ui

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"

	"github.com/penguinpowernz/gosnitch/internal/daemon"
)

// PromptTimeout is how long an untouched prompt waits before applying the
// default. Fixed rather than configurable: it is the window in which an
// unattended machine decides for itself, so it should not drift.
//
// Touching any control in the prompt cancels it entirely - see buildPrompt.
const PromptTimeout = 60 * time.Second

// FallbackDuration is the duration applied when a prompt is not answered.
// Always "once", so a timeout can never create a lasting rule.
const FallbackDuration = daemon.DurationOnce

// Options configure the UI at startup.
type Options struct {
	RulesPath     string
	DefaultAction string
	Interactive   bool // false: never prompt, just record and apply the default
	StartHidden   bool
}

// App wires the tray, the table window and the prompt together.
type App struct {
	fyne      fyne.App
	win       fyne.Window
	table     *tableView
	rules     *rulesView
	tabs      *container.AppTabs
	srv       *daemon.Server
	store     *daemon.Store
	ruleStore *daemon.RuleStore

	defaultAction string
	promptTimeout time.Duration
	interactive   bool

	// Prompts are shown one at a time; pending counts those waiting their
	// turn, including the one on screen, so a burst can be shed rather than
	// queued past the point of usefulness. See App.ask.
	promptMu sync.Mutex
	pending  atomic.Int32

	shown bool
}

func New(store *daemon.Store, srv *daemon.Server, opts Options) *App {
	if opts.DefaultAction == "" {
		opts.DefaultAction = daemon.ActionAllow
	}

	fa := app.NewWithID("nz.co.penguinpower.gosnitch")

	a := &App{
		fyne:          fa,
		store:         store,
		srv:           srv,
		promptTimeout: PromptTimeout,
		interactive:   opts.Interactive,
	}

	// A default chosen from the tray outlives the session; the flag only
	// seeds it the first time gosnitch runs.
	a.defaultAction = fa.Preferences().StringWithFallback(prefDefaultAction, opts.DefaultAction)
	if !validAction(a.defaultAction) {
		a.defaultAction = opts.DefaultAction
	}
	srv.SetDefaults(a.defaultAction, FallbackDuration)

	a.ruleStore = daemon.NewRuleStore(opts.RulesPath)
	a.table = newTableView(store)
	a.rules = newRulesView(a, a.ruleStore)
	a.buildWindow(opts.StartHidden)
	a.buildTray()

	if opts.Interactive {
		srv.SetPrompter(a.ask)
	}

	// Both callbacks arrive on gRPC goroutines; hop to the UI thread.
	store.OnEvent(func() { fyne.Do(a.table.refresh) })
	srv.OnStatus(func() { fyne.Do(a.updateStatus) })

	a.updateStatus()

	// Load the rules off the UI goroutine; the directory holds hundreds of
	// small files and the window should not wait on it.
	go func() {
		a.ruleStore.Reload()
		fyne.Do(a.rules.refresh)
	}()

	return a
}

func (a *App) buildWindow(startHidden bool) {
	a.win = a.fyne.NewWindow("gosnitch")
	a.win.Resize(fyne.NewSize(840, 460))
	a.tabs = container.NewAppTabs(
		container.NewTabItem("Events", a.table.content()),
		container.NewTabItem("Rules", a.rules.content()),
	)
	a.win.SetContent(a.tabs)

	// Closing the window leaves the app running in the tray, like the Python UI.
	a.win.SetCloseIntercept(func() {
		a.win.Hide()
		a.shown = false
	})

	if !startHidden {
		a.shown = true
		a.win.Show()
	}
}

// titleCase uppercases the first letter, for menu labels built from the
// lowercase wire values.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// trayActions are the default actions offered in the tray menu.
var trayActions = []string{daemon.ActionAllow, daemon.ActionDeny}

// prefDefaultAction is the preferences key the tray choice persists under.
const prefDefaultAction = "default_action"

func validAction(a string) bool {
	switch a {
	case daemon.ActionAllow, daemon.ActionDeny, daemon.ActionReject:
		return true
	}
	return false
}

// setDefaultAction records the choice, tells the server, and rebuilds the tray
// so the tick moves to the new item.
func (a *App) setDefaultAction(action string) {
	if !validAction(action) {
		return
	}
	a.defaultAction = action
	a.fyne.Preferences().SetString(prefDefaultAction, action)
	a.srv.SetDefaults(action, FallbackDuration)
	a.buildTray()
}

func (a *App) buildTray() {
	desk, ok := a.fyne.(desktop.App)
	if !ok {
		// No system tray on this platform: keep the window visible instead.
		a.showWindow()
		return
	}

	// Only the default action is configurable. The duration a timeout applies
	// is always "once" and the timeout itself is fixed, so neither can be set
	// to something that would quietly create lasting rules unattended.
	//
	// Reject is deliberately absent: it is a rare choice, and a third item
	// makes the menu something to read rather than a quick switch. It is
	// still accepted from -default-action for anyone who wants it.
	actionItems := make([]*fyne.MenuItem, 0, len(trayActions))
	for _, act := range trayActions {
		act := act
		item := fyne.NewMenuItem(titleCase(act), func() { a.setDefaultAction(act) })
		// Under -default-action reject neither item is ticked, which is
		// accurate: reject is in force and is not one of these. Picking
		// either switches to it.
		item.Checked = a.defaultAction == act
		actionItems = append(actionItems, item)
	}
	defaults := fyne.NewMenuItem("Default action", nil)
	defaults.ChildMenu = fyne.NewMenu("", actionItems...)

	menu := fyne.NewMenu("gosnitch",
		fyne.NewMenuItem("Show events", func() { a.showTab(0) }),
		fyne.NewMenuItem("Manage rules", func() { a.showTab(1) }),
		fyne.NewMenuItemSeparator(),
		defaults,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { a.fyne.Quit() }),
	)
	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayIcon(theme.InfoIcon())
}

// showTab opens the window on a given tab, for the tray menu items.
func (a *App) showTab(i int) {
	if a.tabs != nil && i < len(a.tabs.Items) {
		a.tabs.SelectIndex(i)
	}
	a.showWindow()
}

func (a *App) showWindow() {
	a.win.Show()
	a.win.RequestFocus()
	a.shown = true
}

func (a *App) updateStatus() {
	connected, version, _ := a.srv.Status()
	if connected {
		if version == "" {
			version = "unknown"
		}
		a.table.status.Set(fmt.Sprintf("Connected — daemon %s", version))
		return
	}
	a.table.status.Set("Waiting for opensnitchd…")
}

// Run blocks until the user quits.
func (a *App) Run() { a.fyne.Run() }
