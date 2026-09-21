package terminalimage

import "golang.org/x/sys/unix"

func readFontViewport(fd uintptr) fontViewport {
	size, err := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ)
	if err != nil {
		return fontViewport{}
	}
	return fontViewport{int(size.Col), int(size.Row), int(size.Xpixel), int(size.Ypixel)}
}
