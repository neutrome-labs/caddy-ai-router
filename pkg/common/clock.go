package common

import "time"

// Clock is an interface for time-related functions, useful for testing.
type Clock interface {
	Now() time.Time
}

// SystemClock implements the Clock interface using the system's time.
type SystemClock struct{}

// Now returns the current system time.
func (c SystemClock) Now() time.Time {
	return time.Now()
}

// StaticClock is a mock clock for testing.
type StaticClock struct {
	CurrentTime time.Time
}

// Now returns the mock time.
func (c *StaticClock) Now() time.Time {
	return c.CurrentTime
}

// Advance advances the mock time by the specified duration.
func (c *StaticClock) Advance(d time.Duration) {
	c.CurrentTime = c.CurrentTime.Add(d)
}

// CaddyClock is a global clock instance.
var CaddyClock Clock = SystemClock{}
