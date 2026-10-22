//go:build race

package s3api_test

// raceEnabled reports whether the race detector is built in. sync.Pool drops
// items at random under it, so allocation counts are not comparable then.
const raceEnabled = true
