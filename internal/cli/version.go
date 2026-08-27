package cli

import "runtime/debug"

// version is overridden at build time with:
//
//	go build -ldflags "-X github.com/troglodytto/prizm/internal/cli.version=v1.2.3"
//
// When it is not set — the usual case for `go install pkg@version` — the
// module version baked in by the toolchain is used instead, so a released
// binary can always identify itself with no build flags at all.
var version string

// Version reports the build's version string.
func Version() string {
	if version != "" {
		return version
	}

	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "dev"
	}

	// A build straight from a working tree reports "(devel)".
	if info.Main.Version == "(devel)" {
		return "dev" + vcsSuffix(info)
	}
	return info.Main.Version
}

// vcsSuffix appends the commit a devel build came from, when the toolchain
// stamped one — the difference between "dev" and "dev+dc77005 (dirty)".
func vcsSuffix(info *debug.BuildInfo) string {
	var revision, modified string

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 7 {
				revision = s.Value[:7]
			} else {
				revision = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				modified = " (dirty)"
			}
		}
	}

	if revision == "" {
		return ""
	}
	return "+" + revision + modified
}
