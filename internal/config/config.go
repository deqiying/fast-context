package config

import "time"

const (
	DefaultTreeDepth   = 3
	DefaultMaxTurns    = 3
	DefaultMaxCommands = 8
	DefaultMaxResults  = 10
	DefaultTimeout     = 30 * time.Second
)

func ClampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
