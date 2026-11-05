//go:build !darwin && !windows && !linux

package capture

func noteFront() {}

func clipOwners() []Owner { return nil }
