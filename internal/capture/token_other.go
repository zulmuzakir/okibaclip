//go:build !darwin && !windows && !linux

package capture

func changeToken() (uint64, bool) { return 0, false }
