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

// Duration choices, shown as one row of buttons. The labels are friendlier
// than the wire values the daemon parses.
var durationOptions = []segmentOption{
	{"Once", daemon.DurationOnce},
	{"30 sec", daemon.Duration30s},
	{"5 min", daemon.Duration5m},
	{"1 hour", daemon.Duration1h},
	{"Until reboot", daemon.DurationUntilRestart},
	{"Forever", daemon.DurationAlways},
}

// promptResult carries the user's answer back to the blocked gRPC handler.
type promptResult struct {
	decision daemon.Decision
	ok       bool
}

// maxPendingPrompts caps how many connections may be waiting for a prompt.
//
// Past this, ask declines immediately and the caller applies the default
// action. A burst of unmatched connections is exactly when the user is least
// able to work through a backlog, and a queue longer than this would take
// longer to clear than the daemon is willing to wait anyway.
const maxPendingPrompts = 8

// ask shows the prompt for conn and blocks until the user answers or the
// timeout elapses. It is called from a gRPC goroutine, so all widget work is
// marshalled onto the Fyne goroutine.
//
// Prompts are shown one at a time. Without this a burst of unmatched
// connections opened one stacked window per connection, each with its own
// timeout, which is unanswerable and hides how many decisions are pending.
func (a *App) ask(conn *protocol.Connection) (daemon.Decision, bool) {
	if a.pending.Add(1) > maxPendingPrompts {
		a.pending.Add(-1)
		return daemon.Decision{}, false // caller applies the default
	}
	defer a.pending.Add(-1)

	a.promptMu.Lock()
	defer a.promptMu.Unlock()

	// Buffered so a timeout never leaks the answering goroutine.
	res := make(chan promptResult, 1)

	fyne.Do(func() { a.buildPrompt(conn, res) })

	r := <-res
	return r.decision, r.ok
}

func (a *App) buildPrompt(conn *protocol.Connection, res chan promptResult) {
	win := a.fyne.NewWindow("OpenSnitch")
	win.Resize(fyne.NewSize(620, 420))
	win.CenterOnScreen()

	// stopCountdown is filled in once the buttons exist. Touching any control
	// cancels the timeout: the user is clearly here and deciding, so the
	// prompt should wait for them rather than answer over the top.
	var stopCountdown func()
	touched := func() {
		if stopCountdown != nil {
			stopCountdown()
		}
	}

	// "Once" starts selected: the least committal choice, and the one a
	// timeout would apply anyway.
	dur := newSegmented(durationOptions, FallbackDuration, func(string) { touched() })

	// Scope toggles name the value they pin the rule to, so what each one
	// does is readable without looking anywhere else.
	dest := destLabel(conn)
	destToggle := newToggle(dest, false, touched)
	portToggle := newToggle(fmt.Sprintf("port %d", conn.GetDstPort()), false, touched)
	userToggle := newToggle(fmt.Sprintf("user %d", conn.GetUserId()), false, touched)

	if dest == "" {
		// Nothing to pin to; leave the button visible but inert rather than
		// shifting the layout around.
		destToggle.button.Disable()
	}

	// finish answers the prompt exactly once. Every caller runs on the Fyne
	// goroutine (button taps, the close intercept, and the ticker via
	// fyne.Do), so the answered flag needs no lock and done is closed once.
	answered := false
	done := make(chan struct{})
	finish := func(r promptResult) {
		if answered {
			return
		}
		answered = true
		close(done) // stops the countdown ticker
		res <- r
		win.Close()
	}

	answer := func(action string) {
		finish(promptResult{
			ok: true,
			decision: daemon.Decision{
				Action:   action,
				Duration: dur.Value(),
				Scope: daemon.Scope{
					Dest: destToggle.On(),
					Port: portToggle.On(),
					User: userToggle.On(),
				},
			},
		})
	}

	allow := widget.NewButton("Allow", func() { answer(daemon.ActionAllow) })
	allow.Importance = widget.SuccessImportance
	deny := widget.NewButton("Deny", func() { answer(daemon.ActionDeny) })
	deny.Importance = widget.DangerImportance

	// Closing the window without choosing must still release the daemon.
	win.SetCloseIntercept(func() { finish(promptResult{ok: false}) })

	// See promptKeyHandler: Enter and Space are deliberately inert here.
	win.Canvas().SetOnTypedKey(promptKeyHandler(func() {
		finish(promptResult{ok: false})
	}))
	// Focus nothing, so no widget can receive a key press in the first place.
	win.Canvas().Unfocus()

	// The countdown rides on whichever button the timeout would press, as the
	// Python UI does, so the default action is visible where it will land
	// rather than in a separate line of text.
	deadline := time.Now().Add(a.promptTimeout)
	defaultAction := a.defaultAction
	stopped := make(chan struct{})
	countdownStopped := false

	setCountdown := func() {
		secs := secondsLeft(time.Until(deadline))
		allow.SetText(buttonText("Allow", daemon.ActionAllow, defaultAction, secs))
		deny.SetText(buttonText("Deny", daemon.ActionDeny, defaultAction, secs))
	}
	setCountdown()

	// Cancel the timeout and drop the counter from the button, so it is clear
	// nothing will happen until the user answers.
	stopCountdown = func() {
		if countdownStopped {
			return
		}
		countdownStopped = true
		close(stopped)
		allow.SetText("Allow")
		deny.SetText("Deny")
	}

	win.SetContent(container.NewVBox(
		widget.NewLabelWithStyle(promptHeadline(conn), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel(promptDetail(conn)),
		widget.NewSeparator(),

		widget.NewLabelWithStyle("For how long", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		dur.content(),

		widget.NewLabelWithStyle("Apply to", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Always limited to "+processName(conn)+". Narrow it further:"),
		container.NewGridWithColumns(3, destToggle.button, portToggle.button, userToggle.button),

		widget.NewSeparator(),
		container.NewGridWithColumns(2, deny, allow),
	))

	win.Show()

	// Mirror the daemon's own timeout so a missed prompt is not fatal, and
	// show the time left so the deadline is never a surprise.
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-stopped:
				return
			case <-t.C:
				// select picks randomly among ready channels, so a tick can
				// win the race against a just-closed stopped. Re-check on the
				// UI goroutine, which is the only place countdownStopped is
				// written, so a touched prompt can never time out.
				expired := time.Now().After(deadline)
				fyne.Do(func() {
					if countdownStopped {
						return
					}
					if expired {
						finish(promptResult{ok: false})
						return
					}
					setCountdown()
				})
				if expired {
					return
				}
			}
		}
	}()
}

// secondsLeft rounds up, so the last visible tick is "1" rather than "0".
func secondsLeft(left time.Duration) int {
	if left < 0 {
		return 0
	}
	return int((left + time.Second - 1) / time.Second)
}

// buttonText appends the countdown to the button the timeout would press,
// matching the Python UI's "Allow (12)". The other button keeps its plain
// label, so which one is the default is unambiguous.
func buttonText(label, action, defaultAction string, secs int) string {
	if action != defaultAction {
		return label
	}
	return fmt.Sprintf("%s (%d)", label, secs)
}

// promptKeyHandler makes the keys that could activate a focused button inert.
//
// This is the whole point of the prompt's key handling: a connection prompt can
// appear at any moment, including mid-keystroke while the user is typing
// somewhere else. If Enter reached the Allow button, a rule could be created
// without the user ever seeing the window. Only a deliberate click answers.
// Escape dismisses, which is safe because it applies the default action.
func promptKeyHandler(dismiss func()) func(*fyne.KeyEvent) {
	return func(ev *fyne.KeyEvent) {
		switch ev.Name {
		case fyne.KeyReturn, fyne.KeyEnter, fyne.KeySpace:
			return // deliberately inert
		case fyne.KeyEscape:
			dismiss()
		}
	}
}

// processName is the binary's name, for the headline and the scope toggles.
// Sanitised: it is attacker-controlled, and filepath.Base happily returns a
// name that is nothing but a newline.
func processName(c *protocol.Connection) string {
	// Test for a useless name on the raw string: once sanitised, a name that
	// was nothing but a newline is "\uFFFD", which is not blank and would
	// sail past this check to be shown as the process's name.
	raw := filepath.Base(c.GetProcessPath())
	if strings.TrimSpace(raw) == "" || raw == "." || raw == "/" {
		return fmt.Sprintf("process %d", c.GetProcessId())
	}
	name := safeText(raw)
	if strings.Trim(name, "\uFFFD") == "" {
		// Nothing left but replacement characters: no name worth showing.
		return fmt.Sprintf("process %d", c.GetProcessId())
	}
	return name
}

// destLabel is what the destination toggle pins to: the hostname when known,
// otherwise the IP. Sanitised for the same reason as processName.
//
// Note this is display text only - the value written into a rule comes from
// destOperand on the raw connection, so sanitising here cannot weaken a rule.
func destLabel(c *protocol.Connection) string {
	if h := c.GetDstHost(); strings.TrimSpace(h) != "" {
		return safeText(h)
	}
	return safeText(c.GetDstIp())
}

func promptHeadline(c *protocol.Connection) string {
	dest := destLabel(c)
	if strings.TrimSpace(dest) == "" {
		dest = "an unknown address"
	}
	return fmt.Sprintf("%s wants to connect to %s", processName(c), dest)
}

// promptDetail is the aligned "Field: value" block under the headline. Every
// value here comes from the process being judged, so each is sanitised: an
// unescaped newline in any one of them would forge the lines below it.
func promptDetail(c *protocol.Connection) string {
	host, ip := safeText(c.GetDstHost()), safeText(c.GetDstIp())
	dest := host
	if host != "" && ip != "" {
		dest = fmt.Sprintf("%s (%s)", host, ip)
	} else if host == "" {
		dest = ip
	}

	lines := []string{
		fmt.Sprintf("Path:        %s", safeText(c.GetProcessPath())),
		fmt.Sprintf("Destination: %s:%d  %s", dest, c.GetDstPort(), safeText(strings.ToUpper(c.GetProtocol()))),
		fmt.Sprintf("PID:         %d      User: %d", c.GetProcessId(), c.GetUserId()),
	}
	if args := c.GetProcessArgs(); len(args) > 0 {
		// Joined first, then sanitised as one field, so an argument cannot
		// smuggle a newline in through the join either.
		lines = append(lines, fmt.Sprintf("Command:     %s", safeText(strings.Join(args, " "))))
	}
	return strings.Join(lines, "\n")
}
