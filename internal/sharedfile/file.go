// Package sharedfile is the on-disk format for a shared bag: ordinary .env
// text plus one optional header naming the repos that receive it.
//
// Setting shared variables one --set KEY=VALUE at a time is miserable, so
// every bag is backed by a file and the file is the thing you edit.
package sharedfile

import (
	"strings"

	"github.com/troglodytto/prizm/internal/envfile"
)

// headerPrefix introduces the repo-audience line.
const headerPrefix = "# prizm:repos"

// Render writes a shared bag as editable text.
func Render(repos []string, vars map[string]string) string {
	var b strings.Builder

	if len(repos) > 0 {
		b.WriteString(headerPrefix + " " + strings.Join(repos, ",") + "\n\n")
	}
	b.WriteString(envfile.Render(vars))
	return b.String()
}

// Parse reads a shared bag file. hasHeader distinguishes "no audience stated"
// (membership stays CLI-managed) from "an explicitly empty audience".
func Parse(text string) ([]string, map[string]string, bool, error) {
	var (
		repos     []string
		hasHeader bool
		body      strings.Builder
	)

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))

		if strings.HasPrefix(trimmed, headerPrefix) {
			hasHeader = true
			for _, name := range strings.Split(strings.TrimPrefix(trimmed, headerPrefix), ",") {
				if name = strings.TrimSpace(name); name != "" {
					repos = append(repos, name)
				}
			}
			continue
		}

		body.WriteString(line)
		body.WriteByte('\n')
	}

	vars, err := envfile.Parse(body.String())
	if err != nil {
		return nil, nil, hasHeader, err
	}
	return repos, vars, hasHeader, nil
}
