package models

import (
	"fmt"
	"math"
	"time"
)

const (
	MinNanoTime = int64(math.MinInt64) + 2
	MaxNanoTime = int64(math.MaxInt64) - 1
)

var (
	minNanoTime       = time.Unix(0, MinNanoTime).UTC()
	maxNanoTime       = time.Unix(0, MaxNanoTime).UTC()
	ErrTimeOutOfRange = fmt.Errorf("time outside range %d - %d", MinNanoTime, MaxNanoTime)
)

func SafeCalcTime(timestamp int64, precision string) (time.Time, error) {
	mult := GetPrecisionMultiplier(precision)
	if t, ok := safeSignedMult(timestamp, mult); ok {
		tme := time.Unix(0, t).UTC()
		return tme, CheckTime(tme)
	}

	return time.Time{}, ErrTimeOutOfRange
}

func CheckTime(t time.Time) error {
	if t.Before(minNanoTime) || t.After(maxNanoTime) {
		return ErrTimeOutOfRange
	}
	return nil
}

func safeSignedMult(a, b int64) (int64, bool) {
	if a == 0 || b == 0 || a == 1 || b == 1 {
		return a * b, true
	}
	if a == MinNanoTime || b == MaxNanoTime {
		return 0, false
	}
	c := a * b
	return c, c/b == a
}
