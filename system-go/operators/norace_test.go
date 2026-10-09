//go:build !race

package operators

// raceDetector is true in a build with the race detector, which slows
// regexp matching about forty times over.
const raceDetector = false
