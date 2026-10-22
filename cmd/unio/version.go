package main

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Build metadata. The release build sets these with
// -ldflags "-X github.com/eukalpia/unio/cmd/unio.version=..." (and commit, date).
// They are the only package-level variables in the CLI: the linker writes them
// once and nothing mutates them at run time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// buildInfo is the information printed by "unio version".
type buildInfo struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
	Platform  string
}

// collectBuildInfo merges the link-time values with the information the Go
// toolchain embeds (runtime/debug). Link-time values win when they are set.
func collectBuildInfo(ver, com, dt string, bi *debug.BuildInfo) buildInfo {
	info := buildInfo{
		Version:   ver,
		Commit:    com,
		Date:      dt,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi == nil {
		return info
	}
	if info.Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		info.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "unknown" {
				info.Commit = shortRevision(s.Value)
			}
		case "vcs.time":
			if info.Date == "unknown" {
				info.Date = s.Value
			}
		}
	}
	return info
}

// shortRevision shortens a full commit hash for display.
func shortRevision(rev string) string {
	const short = 12
	if len(rev) > short {
		return rev[:short]
	}
	return rev
}

// newVersionCmd builds "unio version".
func newVersionCmd(a app) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print the build version",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			bi, _ := debug.ReadBuildInfo()
			info := collectBuildInfo(version, commit, date, bi)
			_, err := fmt.Fprintf(a.stdout, "unio version %s\ncommit: %s\nbuilt: %s\ngo: %s\nplatform: %s\n",
				info.Version, info.Commit, info.Date, info.GoVersion, info.Platform)
			return err
		},
	}
}
