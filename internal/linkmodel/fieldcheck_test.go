package linkmodel

import (
	"strings"
	"testing"
)

// CheckValue gives a refused value the very sentence a miss page shows for
// it, so a launcher can say it beside the field before the other values are
// typed (VAR-5).
func TestCheckValueAgreesWithMatch(t *testing.T) {
	const (
		pattern     = "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}/{file}"
		destination = "https://example.com/daily/{file}?month={month}&day={day}"
	)
	meta := []VariableMeta{{Name: "month", Description: "Month, 1 to 12", Example: "10"}}
	p := mustParse(t, pattern)
	tmpl, problems := ParseDestination(destination, p)
	if problems != nil {
		t.Fatal(problems)
	}
	l := link("reports", pattern, destination)
	l.Routes[0].Variables = meta
	c := Compile(l)

	tests := []struct {
		variable, value string
		accepted        bool
		code            string
	}{
		{"month", "10", true, ""},
		{"month", "13", false, ReasonValueRejected},
		{"day", "x", false, ReasonValueRejected},
		{"file", "..", false, ReasonValueNotAllowed},
		{"file", "a/../b", false, ReasonValueNotAllowed},
		{"file", "a.txt", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.variable+"="+tt.value, func(t *testing.T) {
			reason, accepted, declared := CheckValue(p, tmpl, meta, tt.variable, tt.value)
			if !declared || accepted != tt.accepted || reason.Code != tt.code {
				t.Fatalf("CheckValue = %+v, %v, %v; want accepted %v, code %q", reason, accepted, declared, tt.accepted, tt.code)
			}
			if accepted {
				if reason != (Reason{}) {
					t.Errorf("an accepted value has the reason %+v", reason)
				}
				return
			}
			// The same request, with the other values accepted, gets the
			// same reason from Match.
			segments := map[string]string{"month": "10", "day": "13", "file": "a.txt"}
			segments[tt.variable] = tt.value
			_, ok, reasons, err := c.Match([]string{"reports", segments["month"], segments["day"], segments["file"]})
			if err != nil || ok || len(reasons) != 1 {
				t.Fatalf("Match = %v, %v, %v", ok, reasons, err)
			}
			want := reasons[0]
			want.RouteID, want.Pattern = 0, ""
			if reason != want {
				t.Errorf("CheckValue reason\n  %+v\nMatch reason\n  %+v", reason, want)
			}
		})
	}

	if _, _, declared := CheckValue(p, tmpl, meta, "year", "1"); declared {
		t.Error("a variable the pattern does not declare is declared")
	}
	reason, _, _ := CheckValue(p, tmpl, meta, "month", "13")
	if want := `month (Month, 1 to 12) doesn't accept "13". It accepts text matching ^(0?[1-9]|1[0-2])$. For example: "10".`; reason.Message != want {
		t.Errorf("message %q, want %q", reason.Message, want)
	}
}

// RegexProblem speaks of the expression alone. Inside a pattern, an unclosed
// "[" or "(" can only be reported as a missing parenthesis of regex(; on its
// own the expression gets the sentence that says what to fix.
func TestRegexProblem(t *testing.T) {
	tests := []struct {
		source, code, contains string
	}{
		{"[0-9]+", "", ""},
		{"^(0?[1-9]|1[0-2])$", "", ""},
		{"[0-9", CodePatternRegexInvalid, "a character class is opened with [ and never closed with ]"},
		{"(a|b", CodePatternRegexInvalid, "a parenthesis is opened and never closed"},
		{"^(0?[1-9]", CodePatternRegexInvalid, "a parenthesis is opened and never closed"},
		{"a)b", CodePatternRegexInvalid, "a parenthesis is closed that was never opened"},
		{"a**", CodePatternRegexInvalid, "repeats a repetition"},
		{"", CodePatternRegexEmpty, "Write an expression"},
		{"a\nb", CodePatternBadCharacter, "line break"},
		{strings.Repeat("a", MaxRegexLength+1), CodePatternRegexTooLong, "at most"},
	}
	for _, tt := range tests {
		code, message := RegexProblem(tt.source)
		if code != tt.code || !strings.Contains(message, tt.contains) {
			t.Errorf("RegexProblem(%q) = %q, %q; want %q containing %q", tt.source, code, message, tt.code, tt.contains)
		}
		// Whatever it accepts, a pattern accepts in regex( ).
		if code == "" {
			if _, problems := ParsePattern("x/{v:regex(" + tt.source + ")}"); len(problems) != 0 {
				t.Errorf("RegexProblem accepts %q, the pattern refuses it: %v", tt.source, problems)
			}
		}
	}
	// The sentence never mentions the pattern's own syntax.
	for _, source := range []string{"[0-9", "(a|b"} {
		if _, message := RegexProblem(source); strings.Contains(message, "regex(") {
			t.Errorf("RegexProblem(%q) = %q speaks of regex(", source, message)
		}
	}
}
