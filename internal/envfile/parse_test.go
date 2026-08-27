package envfile

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{name: "simple pairs", in: "A=1\nB=two\n", want: map[string]string{"A": "1", "B": "two"}},
		{name: "comments and blank lines ignored", in: "# leading comment\n\nA=1\n   # indented comment\nB=2\n", want: map[string]string{"A": "1", "B": "2"}},
		{name: "export prefix stripped", in: "export A=1\n", want: map[string]string{"A": "1"}},
		{name: "surrounding whitespace trimmed on key and bare value", in: "  A  =  1  \n", want: map[string]string{"A": "1"}},
		{name: "double quotes interpret escapes", in: `A="line1\nline2"` + "\n", want: map[string]string{"A": "line1\nline2"}},
		{name: "single quotes are literal", in: `A='line1\nline2'` + "\n", want: map[string]string{"A": `line1\nline2`}},
		{name: "hash inside quotes is not a comment", in: `A="v#1"` + "\n", want: map[string]string{"A": "v#1"}},
		{name: "equals sign inside value preserved", in: "DSN=postgres://u:p@h/db?a=b\n", want: map[string]string{"DSN": "postgres://u:p@h/db?a=b"}},
		{name: "empty value allowed", in: "A=\n", want: map[string]string{"A": ""}},
		{name: "later duplicate wins", in: "A=1\nA=2\n", want: map[string]string{"A": "2"}},
		{name: "crlf line endings", in: "A=1\r\nB=2\r\n", want: map[string]string{"A": "1", "B": "2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Parse() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantSub string
	}{
		{name: "missing equals", in: "A=1\nNOTAPAIR\n", wantSub: "line 2"},
		{name: "empty key", in: "=value\n", wantSub: "line 1"},
		{name: "unterminated quote", in: `A="oops` + "\n", wantSub: "line 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.in)
			if err == nil {
				t.Fatalf("Parse() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Parse() error = %q, want it to mention %q", err, tt.wantSub)
			}
		})
	}
}
