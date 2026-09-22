//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package terminalimage

func terminalPixelSize(uintptr) Size { return Size{} }
