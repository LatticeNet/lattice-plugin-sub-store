//go:build race

package parse

// raceEnabled reports a test binary built with the race detector, which
// slows every step by a similar factor.
const raceEnabled = true
