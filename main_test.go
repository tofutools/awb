package main

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

// --version reports the commit the binary was built from, and degrades to the
// bare version stamp when the toolchain recorded no commit.
func TestVersionString(t *testing.T) {
	settings := func(pairs ...string) *debug.BuildInfo {
		info := &debug.BuildInfo{}
		for i := 0; i < len(pairs); i += 2 {
			info.Settings = append(info.Settings,
				debug.BuildSetting{Key: pairs[i], Value: pairs[i+1]})
		}
		return info
	}

	for name, tc := range map[string]struct {
		info *debug.BuildInfo
		want string
	}{
		"no build info at all": {
			info: nil,
			want: "1.2.3",
		},
		"build info without a revision": {
			info: settings("-trimpath", "true"),
			want: "1.2.3",
		},
		"a clean checkout": {
			info: settings(
				"vcs.revision", "c80defa13dc1f9ec8dee0b435273910ed67ff871",
				"vcs.time", "2026-08-28T10:13:44Z",
				"vcs.modified", "false"),
			want: "1.2.3 (c80defa13dc1f9ec8dee0b435273910ed67ff871, 2026-08-28T10:13:44Z)",
		},
		"a dirty checkout": {
			info: settings(
				"vcs.revision", "c80defa13dc1f9ec8dee0b435273910ed67ff871",
				"vcs.time", "2026-08-28T10:13:44Z",
				"vcs.modified", "true"),
			want: "1.2.3 (c80defa13dc1f9ec8dee0b435273910ed67ff871, 2026-08-28T10:13:44Z, modified)",
		},
		"a revision with no commit time": {
			info: settings("vcs.revision", "c80defa13dc1f9ec8dee0b435273910ed67ff871"),
			want: "1.2.3 (c80defa13dc1f9ec8dee0b435273910ed67ff871)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, versionString("1.2.3", tc.info))
		})
	}
}
