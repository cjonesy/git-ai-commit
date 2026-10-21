package main

import (
	"runtime/debug"
	"strings"
)

// version is set by release builds: -ldflags "-X main.version=1.2.3".
var version string

// buildVersion returns the release version, or for builds without one (go
// install, go build) the module version Go recorded in the binary.
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "unknown"
}
