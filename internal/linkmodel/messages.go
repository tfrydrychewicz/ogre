package linkmodel

import (
	"strconv"
	"unicode/utf8"
)

// maxQuotedRunes is how much of a piece of user text a message repeats.
// Quoting can write one unprintable character as ten, so the bound on the
// message is ten times this.
const maxQuotedRunes = 40

// quoteForMessage returns s in double quotes for use inside a message.
//
// What it quotes is text someone typed into a pattern or a URL, and for a
// request value it is whatever the URL contained: it may hold line breaks,
// invisible characters or a megabyte of anything. Quoting makes the
// unprintable visible instead of letting it rearrange the sentence, and the
// length is capped so one long value cannot be multiplied by every route's
// reason.
func quoteForMessage(s string) string {
	if utf8.RuneCountInString(s) > maxQuotedRunes {
		cut := 0
		for i := range s {
			if cut == maxQuotedRunes {
				s = s[:i] + "…"
				break
			}
			cut++
		}
	}
	return strconv.Quote(s)
}

// plural returns "1 value" or "3 values".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// HasErrors reports whether any of the problems is an error, as opposed to
// a warning. Errors block saving; warnings do not (DIR-2).
func HasErrors(problems []Problem) bool {
	for _, p := range problems {
		if p.Severity != SeverityWarning {
			return true
		}
	}
	return false
}
