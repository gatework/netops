//go:build !unix

package bootstrap

import "math"

func getLimit() (cur, hard uint64, err error) { return math.MaxUint64, math.MaxUint64, nil }

func setLimit(uint64, uint64) error { return nil }
