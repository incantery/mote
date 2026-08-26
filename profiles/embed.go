// Package profiles holds the profiles that ship with mote, and a copy
// of them compiled into the binary so that `mote demo` works from a
// directory that is not the checkout.
//
// The directories beside this file are the readable original; the
// embedded copy is the same bytes.
package profiles

import (
	"embed"
	"io/fs"

	"github.com/incantery/mote/profile"
)

//go:embed supervisor/profile.md supervisor/policy.toml
var files embed.FS

// FS is the embedded profiles, rooted where the directories are.
func FS() fs.FS { return files }

// Supervisor is the worked example: Vera's rules, as a profile
// directory. It is here so a harness can start with something real
// and edit it, and so the demo has a policy that is not invented in
// the demo.
func Supervisor() (*profile.Profile, error) {
	return profile.LoadFS(files, "supervisor")
}
