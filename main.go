// Command prizm shares environment files across repos, grouped by the workflow
// you want to run.
//
// Running `prizm <group> <workflow>` builds each covered repo's env file from
// shared and per-repo variables, then symlinks it into place.
package main

import (
	"os"

	"github.com/troglodytto/prizm/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
