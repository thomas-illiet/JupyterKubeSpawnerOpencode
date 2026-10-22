// Package version exposes build metadata injected by the release pipeline.
package version

import "runtime/debug"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info returns the configured version, enriched with Go VCS metadata when the
// linker did not inject release values.
func Info() (version, commit, date string) {
	version, commit, date = Version, Commit, Date
	if build, ok := debug.ReadBuildInfo(); ok {
		if version == "dev" && build.Main.Version != "" && build.Main.Version != "(devel)" {
			version = build.Main.Version
		}
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				if commit == "unknown" {
					commit = setting.Value
				}
			case "vcs.time":
				if date == "unknown" {
					date = setting.Value
				}
			}
		}
	}
	return version, commit, date
}
