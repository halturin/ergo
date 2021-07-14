// +build linux freebsd openbsd netbsd dragonfly

package ergo

import (
	"syscall"
)

func os_dep_getResourceUsage() (int64, int64) {
	var usage syscall.Rusage
	var utime, stime int64
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err == nil {
		utime = usage.Utime.Sec*1000000000 + usage.Utime.Nano()
		stime = usage.Stime.Sec*1000000000 + usage.Stime.Nano()
	}
	return utime, stime
}
