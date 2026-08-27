// Command prizm shares environment files across multiple repos, grouped by the
// workflow you want to run.
//
// Running `prizm <group> <workflow>` builds each covered repo's env file from
// shared and per-repo variables, then symlinks it into place.
//
// This is a pre-release scaffold: the command tree is not wired up yet. See
// the implementation plans under docs/ for the full design.
package main

import (
	"fmt"
	"os"
)

// version is overridden at build time with -ldflags "-X main.version=..."
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("prizm", version)
		return
	}

	fmt.Fprintln(os.Stderr, "prizm: not implemented yet — see https://github.com/troglodytto/prizm")
	os.Exit(1)
}
