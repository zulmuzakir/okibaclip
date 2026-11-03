//go:build !darwin && !windows && !linux

package capture

func frontOwner() Owner { return Owner{} }
