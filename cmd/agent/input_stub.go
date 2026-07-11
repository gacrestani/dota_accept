//go:build !windows

package main

import (
	"errors"
	"os"
)

// pressAccept is only implemented on Windows; this stub keeps the agent
// compilable (and the relay round-trip testable) on other platforms.
// Set DOTA_ACCEPT_FAKE=1 to simulate a successful press during development.
func pressAccept() (string, error) {
	if os.Getenv("DOTA_ACCEPT_FAKE") == "1" {
		return "pretended to press Enter (dev stub)", nil
	}
	return "", errors.New("pressing keys into Dota is only implemented in the Windows build of the agent")
}
