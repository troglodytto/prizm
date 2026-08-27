package style

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestMarkGlyphs(t *testing.T) {
	tests := map[Mark]string{
		OK: "✓", Fail: "✗", Warn: "⚠",
		Add: "+", Remove: "-", Change: "~", Same: "=", Ask: "?",
	}

	for mark, want := range tests {
		if got := mark.Glyph(); !strings.Contains(got, want) {
			t.Errorf("Mark(%d).Glyph() = %q, want it to contain %q", mark, got, want)
		}
	}
}

func TestMarkGlyphsAreDistinct(t *testing.T) {
	seen := make(map[string]Mark)

	for _, mark := range []Mark{OK, Fail, Warn, Add, Remove, Change, Same, Ask} {
		glyph := mark.Glyph()
		if other, clash := seen[glyph]; clash {
			t.Errorf("Mark(%d) and Mark(%d) both render %q", mark, other, glyph)
		}
		seen[glyph] = mark
	}
}

func TestRowLayout(t *testing.T) {
	got := Row(OK, "backend", "set (local)")

	if !strings.HasPrefix(got, "✓ ") {
		t.Errorf("Row() = %q, want it to start with the mark", got)
	}
	if !strings.Contains(got, "backend") || !strings.Contains(got, "set (local)") {
		t.Errorf("Row() = %q, want both the name and the detail", got)
	}
}

func TestRowAlignsTheDetailColumn(t *testing.T) {
	short := Row(OK, "ai", "set (local)")
	long := Row(OK, "frontend-app", "set (local)")

	if strings.Index(short, "set (local)") != strings.Index(long, "set (local)") {
		t.Errorf("detail columns do not line up:\n%q\n%q", short, long)
	}
}

// A name wider than the default must widen the column for every row, not go
// ragged — the reason Column is measured rather than fixed.
func TestMeasuredColumnAlignsLongNames(t *testing.T) {
	names := []string{"ai", "auth", "search-svc", "web-frontend"}
	c := WidthOf(names)

	if int(c) != len("search-svc") {
		t.Errorf("WidthOf() = %d, want the widest name (%d)", c, len("search-svc"))
	}

	first := c.Row(OK, names[0], "set (local)")
	for _, n := range names[1:] {
		row := c.Row(OK, n, "set (local)")
		if strings.Index(first, "set (local)") != strings.Index(row, "set (local)") {
			t.Errorf("details misaligned between %q and %q:\n%q\n%q", names[0], n, first, row)
		}
	}
}

func TestWidthOfNeverGoesBelowTheMinimum(t *testing.T) {
	if got := WidthOf([]string{"a", "bc"}); int(got) != MinWidth {
		t.Errorf("WidthOf(short names) = %d, want the %d minimum", got, MinWidth)
	}
	if got := WidthOf(nil); int(got) != MinWidth {
		t.Errorf("WidthOf(nil) = %d, want the %d minimum", got, MinWidth)
	}
}

func TestRowDoesNotTruncateALongName(t *testing.T) {
	name := strings.Repeat("x", MinWidth+8)

	if got := Row(OK, name, "detail"); !strings.Contains(got, name) {
		t.Errorf("Row() = %q, want the full name %q", got, name)
	}
}

func TestRowWithNoDetailHasNoTrailingSpace(t *testing.T) {
	got := Row(Warn, "backend", "")

	if got != strings.TrimRight(got, " ") {
		t.Errorf("Row() = %q, want no trailing whitespace", got)
	}
}

func TestTagColoursAreSemanticAndDistinct(t *testing.T) {
	prod, qa, local := TagColor("prod"), TagColor("qa"), TagColor("local")

	if prod == qa || qa == local || prod == local {
		t.Error("tag colours collide; prod must never look like local")
	}
	if TagColor("something-custom") != nil {
		t.Error("an unknown tag should have no colour of its own")
	}
}

func TestTagRendersTheTagText(t *testing.T) {
	if got := Tag("prod"); !strings.Contains(got, "prod") {
		t.Errorf("Tag() = %q, want the tag text", got)
	}
	if got := Tag(""); got != "" {
		t.Errorf("Tag(\"\") = %q, want empty", got)
	}
	if got := Tag("something-custom"); !strings.Contains(got, "something-custom") {
		t.Errorf("Tag(unknown) = %q, want the text rendered plainly", got)
	}
}

// Colour comes from the terminal's own palette, never a fixed hex value, so
// prizm inherits whatever scheme the user already reads comfortably.
func TestPaletteUsesTerminalColoursNotHex(t *testing.T) {
	for name, c := range map[string]lipgloss.TerminalColor{
		"Red": Red, "Green": Green, "Yellow": Yellow, "Cyan": Cyan, "Base": Base,
	} {
		v, ok := c.(lipgloss.Color)
		if !ok {
			t.Errorf("%s is %T, want lipgloss.Color (an ANSI index)", name, c)
			continue
		}
		if strings.HasPrefix(string(v), "#") {
			t.Errorf("%s = %q, want an ANSI index rather than a hex value", name, v)
		}
	}
}

// Tests and pipes must see plain text, or every substring assertion in every
// phase breaks.
func TestOutputIsPlainWhenNotATerminal(t *testing.T) {
	for _, got := range []string{
		Row(OK, "backend", "set (local)"),
		Heading("XYZ"), Detail("dim"), Hint("run `prizm sync`"), Alert("boom"), Tag("prod"),
	} {
		if strings.Contains(got, "\x1b[") {
			t.Errorf("%q contains escape codes; output must be plain off a terminal", got)
		}
	}
}
