package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
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

// paint marks the selected button. Importance is the only styling hook a plain
// Button exposes, so selection reads as a filled button against outlined ones.
func (s *segmented) paint() {
	for i, b := range s.buttons {
		if i == s.selected {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.MediumImportance
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

// maxToggleLabel caps the value shown on a scope button.
//
// The dest toggle carries whatever hostname the connection used, and a long
// one made the button demand ~845px. The three toggles share a grid, so all
// three inherited that and the prompt stretched to 2544px - far wider than
// the window, leaving a strip of desktop beside it. The untruncated value is
// still on screen in the detail rows just above, so nothing is lost here.
const maxToggleLabel = 28

func newToggle(label string, on bool, onChange func()) *toggle {
	t := &toggle{label: elide(label, maxToggleLabel), on: on}
	t.button = widget.NewButton(label, func() {
		t.on = !t.on
		t.paint()
		if onChange != nil {
			onChange()
		}
	})
	t.paint()
	return t
}

func (t *toggle) paint() {
	if t.on {
		t.button.Importance = widget.HighImportance
		t.button.SetText("✓ " + t.label)
	} else {
		t.button.Importance = widget.LowImportance
		t.button.SetText("   " + t.label)
	}
	t.button.Refresh()
}

func (t *toggle) On() bool { return t.on }

// elide shortens s to at most max runes, cutting the middle rather than the
// end: a hostname's leading label and its TLD both say more about what is
// being allowed than the middle of the string does.
func elide(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	keep := max - 1 // room for the ellipsis
	head := (keep + 1) / 2
	tail := keep - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}
