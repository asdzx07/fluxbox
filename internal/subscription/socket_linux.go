//go:build linux

package subscription

import "syscall"

func setSocketMark(fd uintptr, mark int) {
	_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, mark)
}
