//go:build !linux

package subscription

func setSocketMark(fd uintptr, mark int) {
	// 非 Linux 系统不支持 SO_MARK，直接静默放行
}
