//go:build !darwin && !windows

package capture

func changeToken() (uint64, bool) { return 0, false }
