package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"

	"github.com/penguinpowernz/gosnitch/internal/daemon"
)

// Options configure the UI at startup.
type Options struct {
	RulesPath       string
	DefaultAction   string
	DefaultDuration string
	PromptTimeout   time.Duration
	Interactive     bool // false: never prompt, just record and apply the default
	StartHidden     bool
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

	defaultAction   string
	defaultDuration string
	promptTimeout   time.Duration
	interactive     bool

	shown bool
}

func New(store *daemon.Store, srv *daemon.Server, opts Options) *App {
	if opts.PromptTimeout <= 0 {
		opts.PromptTimeout = 15 * time.Second
	}
	if opts.DefaultDuration == "" {
		opts.DefaultDuration = daemon.DurationOnce
	}
	if opts.DefaultAction == "" {
		opts.DefaultAction = daemon.ActionAllow
	}

	fa := app.NewWithID("nz.co.penguinpower.gosnitch")

	a := &App{
		fyne:            fa,
		store:           store,
		srv:             srv,
		defaultAction:   opts.DefaultAction,
		defaultDuration: opts.DefaultDuration,
		promptTimeout:   opts.PromptTimeout,
		interactive:     opts.Interactive,
	}

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

func (a *App) buildTray() {
	desk, ok := a.fyne.(desktop.App)
	if !ok {
		// No system tray on this platform: keep the window visible instead.
		a.showWindow()
		return
	}

	menu := fyne.NewMenu("gosnitch",
		fyne.NewMenuItem("Show events", func() { a.showTab(0) }),
		fyne.NewMenuItem("Manage rules", func() { a.showTab(1) }),
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
