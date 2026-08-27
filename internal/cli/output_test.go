package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/troglodytto/prizm/internal/style"
)

// Styling applied at each call site is styling that gets forgotten at the
// next one. This test makes that impossible: output.go is the only file in
// the package allowed to write to a terminal, so "is this styled?" has one
// answer instead of one per command.
func TestOutputGoIsTheOnlyWriter(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	writeCall := regexp.MustCompile(`\bfmt\.(Fprint|Fprintln|Fprintf)\b`)

	for _, f := range files {
		base := filepath.Base(f)
		if base == "output.go" || strings.HasSuffix(base, "_test.go") {
			continue
		}

		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}

		for i, line := range strings.Split(string(src), "\n") {
			if writeCall.MatchString(line) {
				t.Errorf("%s:%d writes directly instead of going through output.go:\n\t%s\n"+
					"\tuse app.say/row/result/field/heading/section/detail/hint, or add a helper there",
					base, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// Every helper must produce something. A silent helper is worse than a raw
// print, because nothing reveals it.
func styleColumnForTest() style.Column { return style.WidthOf([]string{"name"}) }

func TestOutputHelpersWrite(t *testing.T) {
	h := newHarness(t)
	col := styleColumnForTest()

	cases := map[string]func(){
		"say":     func() { h.app.say("x") },
		"sayf":    func() { h.app.sayf("%s", "x") },
		"blank":   func() { h.app.blank() },
		"row":     func() { h.app.row(col, 0, "n", "d") },
		"result":  func() { h.app.result(0, "n", "d") },
		"field":   func() { h.app.field(col, "n", "d") },
		"heading": func() { h.app.heading("%s", "x") },
		"section": func() { h.app.section("", "x") },
		"detail":  func() { h.app.detail("%s", "x") },
		"hint":    func() { h.app.hint("%s", "x") },
		"prompt":  func() { h.app.prompt("x") },
	}

	for name, fn := range cases {
		h.out.Reset()
		fn()
		if h.out.Len() == 0 {
			t.Errorf("app.%s wrote nothing", name)
		}
	}
}
