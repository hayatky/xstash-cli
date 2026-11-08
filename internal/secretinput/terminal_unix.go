//go:build linux || darwin

// Package secretinput accepts secrets only through the controlling terminal.
package secretinput

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

type Terminal struct {
	f     *os.File
	state *unix.Termios
	ctx   context.Context
	stop  context.CancelFunc
}

// Open hides input for the complete session, including between prompts. Input
// is never accepted from redirected stdin. Catch ordinary termination/suspend
// signals so Close can discard queued input and restore terminal settings.
func Open(ctx context.Context) (*Terminal, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("token import requires an interactive controlling terminal")
	}
	fd := int(f.Fd())
	old, err := terminalState(fd)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("could not inspect terminal settings")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTSTP)
	hidden := *old
	hidden.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	hidden.Iflag &^= unix.IXON | unix.INLCR | unix.IGNCR
	hidden.Iflag |= unix.ICRNL
	hidden.Cc[unix.VMIN], hidden.Cc[unix.VTIME] = 1, 0
	if err = setTerminalState(fd, &hidden); err != nil {
		stop()
		f.Close()
		return nil, fmt.Errorf("could not disable terminal echo")
	}
	return &Terminal{f: f, state: old, ctx: ctx, stop: stop}, nil
}

func (t *Terminal) Context() context.Context { return t.ctx }

func (t *Terminal) Close() error {
	fd := int(t.f.Fd())
	flushErr := flushInput(fd)
	restoreErr := setTerminalState(fd, t.state)
	closeErr := t.f.Close()
	t.stop()
	if flushErr != nil || restoreErr != nil || closeErr != nil {
		return fmt.Errorf("could not restore terminal cleanly; run stty sane if needed")
	}
	return nil
}

func (t *Terminal) Prompt(ctx context.Context, label string) (result []byte, err error) {
	if _, err = fmt.Fprint(t.f, label); err != nil {
		return nil, fmt.Errorf("terminal output failed")
	}
	buf := make([]byte, 0, 256)
	defer func() {
		if err != nil {
			clear(buf)
		}
		fmt.Fprintln(t.f)
	}()
	fd := int(t.f.Fd())
	one := []byte{0}
	defer clear(one)
	for {
		if ctx.Err() != nil || t.ctx.Err() != nil {
			return nil, fmt.Errorf("input interrupted")
		}
		p := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, e := unix.Poll(p, 100)
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			return nil, fmt.Errorf("terminal poll failed")
		}
		if n == 0 {
			continue
		}
		if p[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
			return nil, fmt.Errorf("terminal disconnected")
		}
		n, e = unix.Read(fd, one)
		if e == unix.EAGAIN || e == unix.EINTR {
			continue
		}
		if e != nil || n != 1 {
			return nil, fmt.Errorf("terminal read failed")
		}
		switch one[0] {
		case '\n', '\r':
			return buf, nil
		case 3, 4, 26:
			return nil, fmt.Errorf("input interrupted") // Ctrl-C/D/Z.
		case 8, 127:
			if len(buf) > 0 {
				buf[len(buf)-1] = 0
				buf = buf[:len(buf)-1]
			}
		default:
			if one[0] < 33 || one[0] > 126 || len(buf) >= 16*1024 {
				return nil, fmt.Errorf("invalid token input")
			}
			buf = append(buf, one[0])
		}
	}
}
