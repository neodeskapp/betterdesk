//go:build !race

package branding

// raceDetector is true in a build with the race detector, which makes the
// regular expressions about twenty times slower.
const raceDetector = false
