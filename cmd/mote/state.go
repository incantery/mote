package main

import (
	"errors"
	"os"
	"path/filepath"
)

// stateDir is where mote keeps things that outlive a run but are not
// the person's documents — conversations, for now. It is the XDG state
// directory: $XDG_STATE_HOME if it is set to an absolute path, and
// ~/.local/state otherwise.
//
// Go's os package has UserConfigDir and UserCacheDir but no state one;
// when it grows os.UserStateDir this function is the thing to delete.
func stateDir() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		if !filepath.IsAbs(d) {
			return "", errors.New("XDG_STATE_HOME is set to a relative path")
		}
		return filepath.Join(d, "mote"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "mote"), nil
}

// sessionDir is stateDir's conversations, or the override a flag gave.
func sessionDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	d, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "sessions"), nil
}
