package secretinput

import "golang.org/x/sys/unix"

func terminalState(fd int) (*unix.Termios, error) { return unix.IoctlGetTermios(fd, unix.TCGETS) }
func setTerminalState(fd int, state *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TCSETS, state)
}
func flushInput(fd int) error { return unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH) }
