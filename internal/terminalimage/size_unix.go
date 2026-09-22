//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package terminalimage

import "golang.org/x/sys/unix"

func terminalPixelSize(fd uintptr) Size {
	winsize, err := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ)
	if err != nil {
		return Size{}
	}
	return Size{
		Columns: int(winsize.Col), Rows: int(winsize.Row),
		PixelWidth: int(winsize.Xpixel), PixelHeight: int(winsize.Ypixel),
	}
}
