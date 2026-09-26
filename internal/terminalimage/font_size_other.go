//go:build !darwin

package terminalimage

func readFontViewport(uintptr) fontViewport { return fontViewport{} }
