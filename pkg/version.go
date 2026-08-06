// Copyright 2026 SSM Annotation Resolver Authors
// SPDX-License-Identifier: Apache-2.0

package pkg

import (
	"runtime/debug"
)

var GitTag = "dev"

func BuildInfo() (string, string, string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", "", ""
	}

	var sha string
	var isDirty bool
	var buildTime string

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			sha = s.Value
		case "vcs.time":
			buildTime = s.Value
		case "vcs.modified":
			isDirty = true
		}
	}

	if isDirty {
		sha += ".dirty"
	}

	return info.Main.Path, sha, buildTime
}
