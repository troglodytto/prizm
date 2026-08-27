package envfile

import "testing"

func TestRenderSortsKeys(t *testing.T) {
	got := Render(map[string]string{"ZED": "1", "ALPHA": "2", "MID": "3"})
	if want := "ALPHA=2\nMID=3\nZED=1\n"; got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestRenderQuoting(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "bare simple", value: "hello", want: "K=hello\n"},
		{name: "bare url", value: "postgres://u:p@h:5432/db?sslmode=disable", want: "K=postgres://u:p@h:5432/db?sslmode=disable\n"},
		{name: "empty stays bare", value: "", want: "K=\n"},
		{name: "space forces quotes", value: "a b", want: "K=\"a b\"\n"},
		{name: "hash forces quotes", value: "a#b", want: "K=\"a#b\"\n"},
		// & would background a command if the file were sourced; ? is inert.
		{name: "ampersand forces quotes", value: "a=1&b=2", want: "K=\"a=1&b=2\"\n"},
		{name: "semicolon forces quotes", value: "a;b", want: "K=\"a;b\"\n"},
		{name: "newline escaped", value: "a\nb", want: `K="a\nb"` + "\n"},
		{name: "tab escaped", value: "a\tb", want: `K="a\tb"` + "\n"},
		{name: "quote escaped", value: `a"b`, want: `K="a\"b"` + "\n"},
		{name: "backslash escaped", value: `a\b`, want: `K="a\\b"` + "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Render(map[string]string{"K": tt.value}); got != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRenderEmptyMap(t *testing.T) {
	if got := Render(map[string]string{}); got != "" {
		t.Errorf("Render(empty) = %q, want %q", got, "")
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	want := map[string]string{
		"SIMPLE": "value", "SPACED": "two words", "MULTI": "line1\nline2",
		"QUOTED": `has "quotes"`, "EMPTY": "", "DSN": "postgres://u:p@h/db?a=b",
	}

	got, err := Parse(Render(want))
	if err != nil {
		t.Fatalf("Parse(Render(v)) error = %v", err)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("round trip %s = %q, want %q", k, got[k], v)
		}
	}
}
