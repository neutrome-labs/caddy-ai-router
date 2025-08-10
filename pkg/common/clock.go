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

// CaddyClock is a global clock instance.
var CaddyClock Clock = SystemClock{}
