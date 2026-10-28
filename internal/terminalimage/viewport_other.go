//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !zos

package terminalimage

func readFontViewport(uintptr) fontViewport { return fontViewport{} }
