//go:build !unix

package sysstat

func statfs(string) (uint64, uint64, bool) { return 0, 0, false }
