package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/penguinpowernz/gosnitch/internal/daemon"
	"github.com/penguinpowernz/gosnitch/internal/protocol"
)

var durationLabels = []string{
	"once",
	"until restart",
	"always",
}

// promptResult carries the user's answer back to the blocked gRPC handler.
type promptResult struct {
	action   string
	duration string
	ok       bool
}

// ask shows a modal asking what to do about conn, and blocks until the user
// answers or timeout elapses. It is called from the gRPC goroutine, so all
// widget work is marshalled onto the Fyne goroutine via fyne.Do.
func (a *App) ask(conn *protocol.Connection) (string, string, bool) {
	// Buffered so a timeout never leaks the answering goroutine.
	res := make(chan promptResult, 1)

	fyne.Do(func() {
		win := a.fyne.NewWindow("OpenSnitch")
		win.Resize(fyne.NewSize(520, 260))
		win.CenterOnScreen()

		dur := widget.NewSelect(durationLabels, nil)
		dur.SetSelected(a.defaultDuration)

		answered := false
		answer := func(action string) {
			if answered {
				return
			}
			answered = true
			res <- promptResult{action: action, duration: dur.Selected, ok: true}
			win.Close()
		}

		allow := widget.NewButton("Allow", func() { answer(daemon.ActionAllow) })
		allow.Importance = widget.HighImportance
		deny := widget.NewButton("Deny", func() { answer(daemon.ActionDeny) })

		// Closing the window without choosing must still release the daemon.
		win.SetCloseIntercept(func() {
			if !answered {
				answered = true
				res <- promptResult{ok: false}
			}
			win.Close()
		})

		win.SetContent(container.NewVBox(
			widget.NewLabelWithStyle(promptHeadline(conn), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			widget.NewSeparator(),
			widget.NewLabel(promptDetail(conn)),
			widget.NewSeparator(),
			container.NewBorder(nil, nil, widget.NewLabel("Duration:"), nil, dur),
			container.NewGridWithColumns(2, deny, allow),
		))
		win.Show()
		win.RequestFocus()

		// Mirror the daemon's own timeout so a missed prompt is not fatal.
		go func() {
			time.Sleep(a.promptTimeout)
			fyne.Do(func() {
				if !answered {
					answered = true
					res <- promptResult{ok: false}
					win.Close()
				}
			})
		}()
	})

	r := <-res
	return r.action, r.duration, r.ok
}

func promptHeadline(c *protocol.Connection) string {
	name := filepath.Base(c.GetProcessPath())
	if name == "" || name == "." {
		name = fmt.Sprintf("process %d", c.GetProcessId())
	}
	dest := c.GetDstHost()
	if dest == "" {
		dest = c.GetDstIp()
	}
	return fmt.Sprintf("%s wants to connect to %s", name, dest)
}

func promptDetail(c *protocol.Connection) string {
	dest := c.GetDstHost()
	if dest != "" && c.GetDstIp() != "" {
		dest = fmt.Sprintf("%s (%s)", dest, c.GetDstIp())
	} else if dest == "" {
		dest = c.GetDstIp()
	}

	lines := []string{
		fmt.Sprintf("Path:        %s", c.GetProcessPath()),
		fmt.Sprintf("Destination: %s:%d  %s", dest, c.GetDstPort(), strings.ToUpper(c.GetProtocol())),
		fmt.Sprintf("PID:         %d      User: %d", c.GetProcessId(), c.GetUserId()),
	}
	if args := c.GetProcessArgs(); len(args) > 0 {
		lines = append(lines, fmt.Sprintf("Command:     %s", strings.Join(args, " ")))
	}
	return strings.Join(lines, "\n")
}
