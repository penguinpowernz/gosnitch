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

// Expiry leads, because it is the column that makes this list worth having:
// the whole point is seeing what is about to lapse and what is not.
var tempRuleColumns = []struct {
	title string
	width float32
}{
	{"Expires", 110},
	{"Action", 70},
	{"Duration", 95},
	{"Process", 250},
	{"Created", 130},
}

// tempTick is how often the countdown is redrawn. The shortest duration on
// offer is 30s, so a second is fine and keeps the column honest without
// repainting the table for no reason.
const tempTick = time.Second

type tempRulesView struct {
	app    *App
	store  *daemon.TempStore
	rules  []daemon.TempRule
	table  *widget.Table
	status binding.String

	selected int // index into rules, -1 for none
	delete   *widget.Button
	stop     chan struct{}
}

func newTempRulesView(a *App, store *daemon.TempStore) *tempRulesView {
	v := &tempRulesView{
		app:      a,
		store:    store,
		status:   binding.NewString(),
		selected: -1,
		stop:     make(chan struct{}),
	}
	v.rules = store.Snapshot(time.Now())

	v.table = widget.NewTable(
		func() (int, int) { return len(v.rules) + 1, len(tempRuleColumns) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			l := o.(*widget.Label)
			if id.Row == 0 {
				l.TextStyle = fyne.TextStyle{Bold: true}
				l.SetText(tempRuleColumns[id.Col].title)
				return
			}
			i := id.Row - 1
			if i >= len(v.rules) {
				l.SetText("")
				return
			}
			l.TextStyle = fyne.TextStyle{Bold: i == v.selected}
			l.SetText(tempRuleCellText(v.rules[i], id.Col, time.Now()))
		},
	)
	for i, c := range tempRuleColumns {
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

// tempRuleCellText renders one cell. Same sanitising as the rules table: the
// process path came from the connection being judged.
func tempRuleCellText(r daemon.TempRule, col int, now time.Time) string {
	switch col {
	case 0:
		return expiryText(r, now)
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
		return r.Created.Format("2006-01-02 15:04")
	}
	return ""
}

// expiryText says when the rule lapses.
//
// "until restart" genuinely has no time we can put here: the daemon drops it
// when it next starts, and nothing tells us when that will be. Showing a blank
// or a zero time would read as "never expires", which is the opposite of true,
// so it is named instead.
func expiryText(r daemon.TempRule, now time.Time) string {
	if !r.HasExpiry() {
		return "on restart"
	}
	left := r.Remaining(now)
	switch {
	case left <= 0:
		return "now"
	case left < time.Minute:
		return fmt.Sprintf("%ds", int(left.Seconds()))
	case left < time.Hour:
		return fmt.Sprintf("%dm %ds", int(left.Minutes()), int(left.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %dm", int(left.Hours()), int(left.Minutes())%60)
	}
}

// refresh pulls a new snapshot. Must run on the Fyne goroutine.
//
// Snapshot drops what has expired, so a rule the daemon has already forgotten
// leaves the table on its own rather than sitting there offering a Delete that
// would fail.
func (v *tempRulesView) refresh() {
	var selectedName string
	if v.selected >= 0 && v.selected < len(v.rules) {
		selectedName = v.rules[v.selected].Name
	}

	v.rules = v.store.Snapshot(time.Now())

	// Entries expire out from under the list, so an index is not a stable
	// handle on a selection; re-find it by name.
	v.selected = -1
	if selectedName != "" {
		for i, r := range v.rules {
			if r.Name == selectedName {
				v.selected = i
				break
			}
		}
	}

	v.updateStatus()
	v.table.Refresh()
}

func (v *tempRulesView) updateStatus() {
	if v.selected >= 0 && v.selected < len(v.rules) {
		v.status.Set(fmt.Sprintf("%s — selected: %s",
			tempRuleCount(len(v.rules)), safeText(v.rules[v.selected].Name)))
		return
	}
	if len(v.rules) == 0 {
		// Say why it is empty. The list only holds what this gosnitch answered,
		// so an empty table is the normal state, not a fault.
		v.status.Set("No temporary rules — answering a prompt with anything but Forever adds one")
		return
	}
	v.status.Set(tempRuleCount(len(v.rules)) + " — select one to delete")
}

func tempRuleCount(n int) string {
	if n == 1 {
		return "1 temporary rule"
	}
	return fmt.Sprintf("%d temporary rules", n)
}

// confirmDelete asks before removing, matching the rules tab.
func (v *tempRulesView) confirmDelete() {
	if v.selected < 0 || v.selected >= len(v.rules) {
		return
	}
	rule := v.rules[v.selected]

	if connected, _, _ := v.app.srv.Status(); !connected {
		dialog.ShowError(daemon.ErrNoDaemon, v.app.win)
		return
	}

	msg := widget.NewLabel(fmt.Sprintf(
		"%s\n\nAction:      %s %s\nProcess:  %s\nExpires:   %s\n\nThis cannot be undone.",
		safeText(rule.Name), safeText(rule.Action), safeText(rule.Duration),
		safeText(rule.Process), expiryText(rule, time.Now())))
	msg.Wrapping = fyne.TextWrapWord

	d := dialog.NewCustomConfirm("Delete temporary rule?", "Delete", "Cancel", msg, func(ok bool) {
		if ok {
			v.doDelete(rule)
		}
	}, v.app.win)
	d.Resize(fyne.NewSize(460, 280))
	d.Show()
}

func (v *tempRulesView) doDelete(rule daemon.TempRule) {
	v.delete.Disable()
	v.status.Set(fmt.Sprintf("Deleting %s…", rule.Name))

	// Same round trip as a permanent rule: DELETE_RULE names the rule, and the
	// daemon removes it from the loader whether or not it has a file on disk.
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
			v.store.Remove(rule.Name)
			v.refresh()
		})
	}()
}

func (v *tempRulesView) content() fyne.CanvasObject {
	v.delete = widget.NewButtonWithIcon("Delete rule", theme.DeleteIcon(), v.confirmDelete)
	v.delete.Importance = widget.DangerImportance

	bar := container.NewBorder(nil, nil,
		widget.NewLabelWithData(v.status),
		container.NewHBox(v.delete),
	)
	return container.NewBorder(nil, bar, nil, nil, v.table)
}

// startTicker redraws the countdown and retires expired rules. The daemon is
// the authority on expiry; this only keeps the table from disagreeing with it.
func (v *tempRulesView) startTicker() {
	go func() {
		t := time.NewTicker(tempTick)
		defer t.Stop()
		for {
			select {
			case <-v.stop:
				return
			case <-t.C:
				fyne.Do(v.refresh)
			}
		}
	}()
}
