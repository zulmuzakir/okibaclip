package main

import "runtime"

var (
	isDarwin = runtime.GOOS == "darwin"
	isLinux  = runtime.GOOS == "linux"
)
