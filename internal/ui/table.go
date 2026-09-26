package ui

import (
	"fmt"
	"path/filepath"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/penguinpowernz/gosnitch/internal/daemon"
)

// The simple table: time, what was decided, which program, and where to.
var columns = []struct {
	title string
	width float32
}{
	{"Time", 90},
	{"Action", 80},
	{"Process", 260},
	{"Destination", 240},
	{"Port", 70},
	{"Proto", 70},
}

type tableView struct {
	store  *daemon.Store
	rows   []daemon.Entry
	table  *widget.Table
	status binding.String
}

func newTableView(store *daemon.Store) *tableView {
	v := &tableView{store: store, status: binding.NewString()}
	v.rows = store.Snapshot()

	v.table = widget.NewTable(
		func() (int, int) { return len(v.rows) + 1, len(columns) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			l := o.(*widget.Label)
			if id.Row == 0 {
				l.TextStyle = fyne.TextStyle{Bold: true}
				l.SetText(columns[id.Col].title)
				return
			}
			l.TextStyle = fyne.TextStyle{}
			i := id.Row - 1
			if i >= len(v.rows) {
				l.SetText("")
				return
			}
			l.SetText(cellText(v.rows[i], id.Col))
		},
	)
	for i, c := range columns {
		v.table.SetColumnWidth(i, c.width)
	}
	v.table.StickyRowCount = 1

	return v
}

// cellText renders one cell. The process path, destination and protocol come
// straight off the wire from the process being reported, so each is sanitised
// before it reaches a label.
func cellText(e daemon.Entry, col int) string {
	switch col {
	case 0:
		return e.Time.Format("15:04:05")
	case 1:
		return safeText(e.Action)
	case 2:
		// Full path is long and the interesting part is the binary name.
		if e.Process == "" {
			return fmt.Sprintf("pid %d", e.PID)
		}
		return safeText(filepath.Base(e.Process))
	case 3:
		return safeText(e.Dest)
	case 4:
		return strconv.FormatUint(uint64(e.Port), 10)
	case 5:
		return safeText(e.Proto)
	}
	return ""
}

// refresh pulls a new snapshot. Must run on the Fyne goroutine.
func (v *tableView) refresh() {
	v.rows = v.store.Snapshot()
	v.table.Refresh()
}

func (v *tableView) content() fyne.CanvasObject {
	statusLabel := widget.NewLabelWithData(v.status)

	clear := widget.NewButtonWithIcon("Clear", theme.ContentClearIcon(), func() {
		v.store.Clear()
	})

	bar := container.NewBorder(nil, nil, statusLabel, clear)
	return container.NewBorder(nil, bar, nil, nil, v.table)
}
