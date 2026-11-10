package main

import (
	"strings"
	"unicode"
)

// maxConsecutiveLineBreaks bounds blank-line runs in stored multi-line text so
// a pasted body cannot scroll another user's screen with newlines alone.
const maxConsecutiveLineBreaks = 2

// terminalHostileRunes are printable-but-invisible characters that can reorder,
// hide, or split text once it reaches a terminal: bidi overrides and isolates,
// directional marks, zero-width space, word joiner, soft hyphen, the byte-order
// mark, and Unicode line/paragraph separators. C0/C1 controls, DEL and ESC are
// covered separately by unicode.IsControl.
//
// ZWJ (U+200D) and ZWNJ (U+200C) are deliberately kept: they are required for
// correct shaping and emoji sequences in scripts this BBS serves.
var terminalHostileRunes = map[rune]struct{}{
	'\u00ad': {}, // soft hyphen
	'\u061c': {}, // arabic letter mark
	'\u200b': {}, // zero width space
	'\u200e': {}, // left-to-right mark
	'\u200f': {}, // right-to-left mark
	'\u2028': {}, // line separator
	'\u2029': {}, // paragraph separator
	'\u202a': {}, // left-to-right embedding
	'\u202b': {}, // right-to-left embedding
	'\u202c': {}, // pop directional formatting
	'\u202d': {}, // left-to-right override
	'\u202e': {}, // right-to-left override
	'\u2060': {}, // word joiner
	'\u2066': {}, // left-to-right isolate
	'\u2067': {}, // right-to-left isolate
	'\u2068': {}, // first strong isolate
	'\u2069': {}, // pop directional isolate
	'\ufeff': {}, // zero width no-break space / byte-order mark
}

// sanitizeTerminalText is the single rule for what user-supplied text may keep.
// It strips everything that could drive the receiving terminal (ESC, C0/C1
// controls, DEL), drops the invisible bidi/zero-width characters above, and
// normalises line breaks: CRLF and lone CR become LF, tabs become spaces, and
// blank-line runs are capped. Multi-line text keeps its line breaks; single-line
// text turns them into spaces.
//
// The function is idempotent, so applying it at more than one boundary (form
// input, persistence, load) cannot corrupt content.
func sanitizeTerminalText(text string, multiline bool) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ToValidUTF8(text, string(unicode.ReplacementChar))
	var b strings.Builder
	b.Grow(len(text))
	breaks := 0
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r':
			if !multiline {
				b.WriteByte(' ')
				breaks = 0
				continue
			}
			if breaks < maxConsecutiveLineBreaks {
				b.WriteByte('\n')
			}
			breaks++
			continue
		case r == '\t':
			b.WriteByte(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		if _, hostile := terminalHostileRunes[r]; hostile {
			continue
		}
		breaks = 0
		b.WriteRune(r)
	}
	return b.String()
}

// sanitizeSingleLineText cleans text that renders on one line (subjects, names,
// board titles) and collapses internal whitespace runs.
func sanitizeSingleLineText(text string) string {
	return strings.Join(strings.Fields(sanitizeTerminalText(text, false)), " ")
}

// sanitizeMultilineText cleans text that may keep paragraph breaks (message
// bodies, bulletins) and trims surrounding whitespace.
func sanitizeMultilineText(text string) string {
	return strings.TrimSpace(sanitizeTerminalText(text, true))
}

// sanitizeFieldText applies the right rule for a form field kind. Passwords and
// choice values are returned unchanged: passwords are never rendered and must
// stay byte-exact so verification keeps working.
func sanitizeFieldText(kind fieldKind, text string) string {
	switch kind {
	case fieldTextArea:
		return sanitizeMultilineText(text)
	case fieldText:
		return sanitizeSingleLineText(text)
	default:
		return text
	}
}
