package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// segmented is a row of buttons where exactly one is selected, standing in for
// a radio group. Buttons are a much larger target than a radio dot, which is
// the point: a security prompt should be answerable without careful aiming.
type segmented struct {
	buttons  []*widget.Button
	values   []string
	selected int
	onChange func(string)
}

type segmentOption struct {
	label string
	value string
}

func newSegmented(opts []segmentOption, selected string, onChange func(string)) *segmented {
	s := &segmented{onChange: onChange, selected: -1}

	for i, opt := range opts {
		i, value := i, opt.value
		b := widget.NewButton(opt.label, nil)
		b.OnTapped = func() { s.selectIndex(i) }
		s.buttons = append(s.buttons, b)
		s.values = append(s.values, value)
		if value == selected {
			s.selected = i
		}
	}
	if s.selected < 0 && len(s.buttons) > 0 {
		s.selected = 0
	}
	s.paint()
	return s
}

func (s *segmented) selectIndex(i int) {
	if i == s.selected {
		return
	}
	s.selected = i
	s.paint()
	if s.onChange != nil {
		s.onChange(s.values[i])
	}
}

// How a chosen option is distinguished from an unchosen one. Importance is
// the only styling hook a plain Button exposes, so selection reads as a
// filled button against outlined ones. Shared by both rows of the prompt so
// they stay the same colour as each other.
const (
	selectedImportance   = widget.HighImportance
	unselectedImportance = widget.MediumImportance
)

// paint marks the selected button.
func (s *segmented) paint() {
	for i, b := range s.buttons {
		if i == s.selected {
			b.Importance = selectedImportance
		} else {
			b.Importance = unselectedImportance
		}
		b.Refresh()
	}
}

// Value returns the selected value.
func (s *segmented) Value() string {
	if s.selected < 0 || s.selected >= len(s.values) {
		return ""
	}
	return s.values[s.selected]
}

// maxSegmentColumns caps how many options share a row.
//
// Six duration buttons in a single row asked for 624px, which set the
// prompt's minimum width on its own and pushed the window past the 620 it is
// resized to. Wrapping to two rows of three keeps each button a large target
// without the row dictating the window's width.
const maxSegmentColumns = 3

func (s *segmented) content() fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, len(s.buttons))
	for i, b := range s.buttons {
		objs[i] = b
	}
	cols := len(objs)
	if cols > maxSegmentColumns {
		cols = maxSegmentColumns
	}
	// Equal widths keep every option the same size target.
	return container.NewGridWithColumns(cols, objs...)
}

// toggle is an independent on/off button, used for the scope options. It shows
// the value it narrows to, so what is being toggled is visible on the button.
type toggle struct {
	button *widget.Button
	label  string
	on     bool
}

// maxToggleWidth caps how wide the value on a scope button may render.
//
// The dest toggle carries whatever hostname the connection used. The three
// toggles share a grid, which gives every cell the width of the widest, so
// one long name set the width of all three and stretched the prompt past its
// window - the strip of desktop beside it. The untruncated hostname is still
// on screen in the detail rows just above, so nothing is lost by cutting it
// down here.
//
// The budget is a third of the window, less what the button draws around the
// text and the padding the grid puts between cells. It is a rendered width
// rather than a rune count because a count is the wrong unit for a hostname
// that may be an IDN: twenty wide glyphs render nearly twice as wide as
// twenty of "sub.". TestScopeRowFitsThePrompt checks the arithmetic against
// the real widgets.
const (
	scopeColumns = 3
	toggleChrome = 34 // button padding, the tick's spacer, and inner border
	gridGutters  = 24 // padding the grid puts between and around cells

	maxToggleWidth float32 = (promptWidth-gridGutters)/scopeColumns - toggleChrome
)

func newToggle(label string, on bool, onChange func()) *toggle {
	t := &toggle{label: elideToWidth(label, maxToggleWidth), on: on}
	// No text here: paint sets it, prefixed with the tick or its spacer.
	t.button = widget.NewButton("", func() {
		t.on = !t.on
		t.paint()
		if onChange != nil {
			onChange()
		}
	})
	t.paint()
	return t
}

// paint marks the toggle on or off, using the same two importances as
// segmented.paint. Both rows are the same kind of control - pick an option,
// see it filled in - so a different unselected colour for each read as a
// difference in meaning rather than in kind.
func (t *toggle) paint() {
	if t.on {
		t.button.Importance = selectedImportance
		t.button.SetText("✓ " + t.label)
	} else {
		t.button.Importance = unselectedImportance
		t.button.SetText("   " + t.label)
	}
	t.button.Refresh()
}

func (t *toggle) On() bool { return t.on }

// elide shortens s until it renders no wider than max, cutting the middle
// rather than the end: a hostname's leading label and its TLD both say more
// about what is being allowed than the middle of the string does.
func elideToWidth(s string, max float32) string {
	if textWidth(s) <= max {
		return s
	}
	r := []rune(s)
	// Shrink the kept runes until what is left fits. Each step drops one from
	// whichever side currently has more, so the cut stays near the middle.
	for keep := len(r) - 1; keep > 0; keep-- {
		head := (keep + 1) / 2
		tail := keep - head
		candidate := string(r[:head]) + "…" + string(r[len(r)-tail:])
		if textWidth(candidate) <= max {
			return candidate
		}
	}
	return "…"
}

// textWidth is the width a button label renders at. Buttons use the standard
// text size and an unstyled face.
func textWidth(s string) float32 {
	return fyne.MeasureText(s, theme.TextSize(), fyne.TextStyle{}).Width
}
