package style

import (
	"strings"
	"testing"
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

func TestRowDoesNotTruncateALongName(t *testing.T) {
	name := strings.Repeat("x", NameWidth+8)

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
	if TagColor("something-custom") != TagColor("") {
		t.Error("an unknown tag should render like an untagged one")
	}
}

func TestTagRendersTheTagText(t *testing.T) {
	if got := Tag("prod"); !strings.Contains(got, "prod") {
		t.Errorf("Tag() = %q, want the tag text", got)
	}
	if got := Tag(""); got != "" {
		t.Errorf("Tag(\"\") = %q, want empty", got)
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
