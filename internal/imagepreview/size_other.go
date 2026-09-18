//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package imagepreview

func terminalPixelSize(uintptr) Size { return Size{} }
