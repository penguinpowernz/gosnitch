package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxDisplayLen caps one displayed field. Long enough for a real path or
// command line, short enough that a padded string cannot push the rest of the
// prompt off screen.
const maxDisplayLen = 200

// safeText makes an attacker-controlled string safe to put in a label.
//
// Everything the prompt shows about a connection - the process path, its
// arguments, the destination host - comes from the process being judged, and a
// prompt is laid out as aligned lines of "Field: value". A value containing a
// newline therefore forges whole lines: a host of
// "ok.example.com\n\nPath: /usr/bin/trusted" makes the prompt attribute the
// connection to a binary that is not making it. Since the point of the prompt
// is to show the user what they are approving, a field that can rewrite the
// prompt defeats it entirely.
//
// So: no control characters (they become U+FFFD rather than vanishing, so
// tampering is visible rather than silent), no bidi overrides, and a length
// cap. The result is always a single line.
func safeText(s string) string {
	if s == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(s))
	n := 0
	truncated := false

	for _, r := range s {
		if n >= maxDisplayLen {
			truncated = true
			break
		}
		switch {
		case r == utf8.RuneError:
			// Invalid UTF-8 in the source; range already yields RuneError.
			b.WriteRune('�')
		case unicode.IsControl(r), isBidiControl(r):
			// Includes \n, \r and \t, which are what forge lines and columns.
			b.WriteRune('�')
		default:
			b.WriteRune(r)
		}
		n++
	}

	out := b.String()
	if truncated {
		out += "…"
	}
	return out
}

// isBidiControl reports whether r re-orders the text around it. Without this a
// path can be made to render right-to-left and read as a different path
// entirely, which is the same spoof by another route.
func isBidiControl(r rune) bool {
	switch r {
	case '‎', '‏', // LRM, RLM
		'‪', '‫', '‬', '‭', '‮', // embedding/override
		'⁦', '⁧', '⁨', '⁩': // isolates
		return true
	}
	return false
}
