package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/penguinpowernz/gosnitch/internal/daemon"
)

var ruleColumns = []struct {
	title string
	width float32
}{
	{"Created", 130},
	{"Action", 70},
	{"Duration", 95},
	{"Process", 230},
	{"Destination", 200},
	{"Port", 60},
}

type rulesView struct {
	app    *App
	store  *daemon.RuleStore
	rules  []daemon.Rule
	table  *widget.Table
	status binding.String

	selected int // index into rules, -1 for none
	delete   *widget.Button
}

func newRulesView(a *App, store *daemon.RuleStore) *rulesView {
	v := &rulesView{
		app:      a,
		store:    store,
		status:   binding.NewString(),
		selected: -1,
	}
	v.rules = store.Snapshot()

	v.table = widget.NewTable(
		func() (int, int) { return len(v.rules) + 1, len(ruleColumns) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			l := o.(*widget.Label)
			if id.Row == 0 {
				l.TextStyle = fyne.TextStyle{Bold: true}
				l.SetText(ruleColumns[id.Col].title)
				return
			}
			i := id.Row - 1
			if i >= len(v.rules) {
				l.SetText("")
				return
			}
			// Mark the selected row so it is obvious what Delete will remove.
			l.TextStyle = fyne.TextStyle{Bold: i == v.selected}
			l.SetText(ruleCellText(v.rules[i], id.Col))
		},
	)
	for i, c := range ruleColumns {
		v.table.SetColumnWidth(i, c.width)
	}
	v.table.StickyRowCount = 1

	v.table.OnSelected = func(id widget.TableCellID) {
		if id.Row == 0 {
			return // header
		}
		v.selected = id.Row - 1
		v.updateStatus()
		v.table.Refresh()
	}

	return v
}

// ruleCellText renders one cell. Every string field is sanitised: rule files
// are built from connection data, so a crafted process path or hostname would
// otherwise put control characters into the table.
func ruleCellText(r daemon.Rule, col int) string {
	switch col {
	case 0:
		return r.Created.Format("2006-01-02 15:04")
	case 1:
		return safeText(r.Action)
	case 2:
		return safeText(r.Duration)
	case 3:
		if r.Process == "" {
			return "—"
		}
		return safeText(filepath.Base(r.Process))
	case 4:
		if r.Dest == "" {
			return "—"
		}
		return safeText(r.Dest)
	case 5:
		if r.Port == "" {
			return "—"
		}
		return safeText(r.Port)
	}
	return ""
}

// refresh pulls a new snapshot. Must run on the Fyne goroutine.
func (v *rulesView) refresh() {
	v.rules = v.store.Snapshot()
	if v.selected >= len(v.rules) {
		v.selected = -1
	}
	v.updateStatus()
	v.table.Refresh()
}

func (v *rulesView) updateStatus() {
	if err := v.store.Err(); err != nil {
		v.status.Set(fmt.Sprintf("Cannot read rules: %v", err))
		return
	}
	// Unparseable rules are still enforced by the daemon, so say so rather
	// than letting the table imply they do not exist.
	var warn string
	if n := v.store.Skipped(); n == 1 {
		warn = " — 1 rule unreadable"
	} else if n > 1 {
		warn = fmt.Sprintf(" — %d rules unreadable", n)
	}

	if v.selected >= 0 && v.selected < len(v.rules) {
		v.status.Set(fmt.Sprintf("%d rules — selected: %s%s", len(v.rules), safeText(v.rules[v.selected].Name), warn))
		return
	}
	v.status.Set(fmt.Sprintf("%d rules — select one to delete%s", len(v.rules), warn))
}

// confirmDelete asks before removing, because deletion cannot be undone.
func (v *rulesView) confirmDelete() {
	if v.selected < 0 || v.selected >= len(v.rules) {
		return
	}
	rule := v.rules[v.selected]

	if connected, _, _ := v.app.srv.Status(); !connected {
		dialog.ShowError(daemon.ErrNoDaemon, v.app.win)
		return
	}

	// Rule fields come from files the daemon wrote from connection data, so
	// they are attacker-influenced the same way the prompt's fields are, and
	// this dialog is laid out the same way. Sanitise before showing.
	msg := widget.NewLabel(fmt.Sprintf(
		"%s\n\nAction:      %s %s\nProcess:  %s\n\nThis cannot be undone.",
		safeText(rule.Name), safeText(rule.Action), safeText(rule.Duration),
		safeText(rule.Process)))
	msg.Wrapping = fyne.TextWrapWord

	d := dialog.NewCustomConfirm("Delete rule?", "Delete", "Cancel", msg, func(ok bool) {
		if ok {
			v.doDelete(rule)
		}
	}, v.app.win)
	d.Resize(fyne.NewSize(460, 260))
	d.Show()
}

func (v *rulesView) doDelete(rule daemon.Rule) {
	v.delete.Disable()
	v.status.Set(fmt.Sprintf("Deleting %s…", rule.Name))

	// The daemon call blocks on a round trip, so keep it off the UI goroutine.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := v.app.srv.DeleteRule(ctx, rule.Name)

		fyne.Do(func() {
			v.delete.Enable()
			if err != nil {
				dialog.ShowError(err, v.app.win)
				v.updateStatus()
				return
			}
			// The daemon has removed the file; drop it locally so the table
			// updates now rather than waiting for the next reload.
			v.store.Remove(rule.Name)
			v.selected = -1
			v.refresh()
		})
	}()
}

func (v *rulesView) content() fyne.CanvasObject {
	v.delete = widget.NewButtonWithIcon("Delete rule", theme.DeleteIcon(), v.confirmDelete)
	v.delete.Importance = widget.DangerImportance

	reload := widget.NewButtonWithIcon("Reload", theme.ViewRefreshIcon(), func() {
		go func() {
			v.store.Reload()
			fyne.Do(v.refresh)
		}()
	})

	bar := container.NewBorder(nil, nil,
		widget.NewLabelWithData(v.status),
		container.NewHBox(reload, v.delete),
	)
	return container.NewBorder(nil, bar, nil, nil, v.table)
}
