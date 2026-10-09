//go:build race

package main

// raceEnabled reports a test binary built with the race detector, which
// allocates and slows every step on its own account.
const raceEnabled = true
