//go:build !linux

package main

import "os"

// Refusing is the safe answer where the terminal check is not implemented:
// the commands that need a human then do not run.
func isTerminal(*os.File) bool { return false }
