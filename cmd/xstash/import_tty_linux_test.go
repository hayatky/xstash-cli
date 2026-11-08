package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/lock"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"golang.org/x/sys/unix"
)

func TestImportTTYChild(t *testing.T) {
	if os.Getenv("XSTASH_TEST_TTY_CHILD") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if err := run(context.Background(), os.Args[i+1:], os.Stdout, os.Stderr); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
	os.Exit(3)
}

type importTTY struct {
	master     int
	slave      *os.File
	cmd        *exec.Cmd
	transcript strings.Builder
	home       string
}

func newImportTTY(t *testing.T, extra ...string) *importTTY {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	home := t.TempDir()
	args := []string{"-test.run=^TestImportTTYChild$", "--", "config", "import", "--client-id", "synthetic-public", "--scopes", xapi.RequiredScope}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], append(args, extra...)...)
	cmd.Env = append(os.Environ(), "XSTASH_TEST_TTY_CHILD=1", "XSTASH_HOME="+home, "XSTASH_CLIENT_ID=", "XSTASH_CLIENT_SECRET=", "XSTASH_ACCESS_TOKEN=", "XSTASH_REFRESH_TOKEN=")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	return &importTTY{master: fd, slave: slave, cmd: cmd, home: home}
}
func (p *importTTY) read(t *testing.T, timeout time.Duration) {
	t.Helper()
	fds := []unix.PollFd{{Fd: int32(p.master), Events: unix.POLLIN}}
	_, err := unix.Poll(fds, int(timeout/time.Millisecond))
	if err != nil && err != unix.EINTR {
		t.Fatal(err)
	}
	if fds[0].Revents&unix.POLLIN != 0 {
		buf := make([]byte, 4096)
		n, e := unix.Read(p.master, buf)
		if e != nil && e != unix.EAGAIN {
			t.Fatal(e)
		}
		if n > 0 {
			p.transcript.Write(buf[:n])
		}
	}
}
func (p *importTTY) expect(t *testing.T, value string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(p.transcript.String(), value) {
		if time.Now().After(deadline) {
			t.Fatalf("missing prompt/result %q", value)
		}
		p.read(t, 100*time.Millisecond)
	}
}
func (p *importTTY) send(t *testing.T, value string) {
	t.Helper()
	if _, err := unix.Write(p.master, []byte(value)); err != nil {
		t.Fatal(err)
	}
}
func (p *importTTY) hidden(t *testing.T) {
	t.Helper()
	st, err := unix.IoctlGetTermios(int(p.slave.Fd()), unix.TCGETS)
	if err != nil || st.Lflag&(unix.ECHO|unix.ECHONL) != 0 {
		t.Fatal("terminal echo enabled during input")
	}
}
func (p *importTTY) restored(t *testing.T) {
	t.Helper()
	st, err := unix.IoctlGetTermios(int(p.slave.Fd()), unix.TCGETS)
	if err != nil || st.Lflag&unix.ECHO == 0 || st.Lflag&unix.ICANON == 0 {
		t.Fatal("terminal settings not restored")
	}
}
func (p *importTTY) assertPrivate(t *testing.T) {
	t.Helper()
	p.read(t, 100*time.Millisecond)
	for _, v := range []string{"synthetic-private-access", "synthetic-private-refresh", "synthetic-partial", "synthetic-leftover"} {
		if strings.Contains(p.transcript.String(), v) {
			t.Fatal("token appeared in terminal output")
		}
	}
}

func TestImportTTYHiddenAndRestored(t *testing.T) {
	p := newImportTTY(t)
	p.expect(t, "access token (hidden): ")
	p.hidden(t)
	p.send(t, "synthetic-private-access\n")
	p.expect(t, "refresh token (hidden): ")
	p.hidden(t)
	for _, name := range []string{"cmdline", "environ"} {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/%s", p.cmd.Process.Pid, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "synthetic-private-access") {
			t.Fatal("token found in process arguments or environment")
		}
	}
	p.send(t, "synthetic-private-refresh\nsynthetic-leftover\n")
	if err := p.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	p.expect(t, `"credentials_imported": true`)
	p.assertPrivate(t)
	p.restored(t)
	fds := []unix.PollFd{{Fd: int32(p.slave.Fd()), Events: unix.POLLIN}}
	if n, err := unix.Poll(fds, 0); err != nil || n != 0 {
		t.Fatal("queued secret input was not flushed")
	}
	path := filepath.Join(p.home, "config.json")
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessToken != "synthetic-private-access" || c.RefreshToken != "synthetic-private-refresh" || c.ScopeSource != "user_selected" || c.ExpiresAt.After(time.Now()) {
		t.Fatal("wrong saved credentials or metadata")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal("credential file not private")
	}
}
func TestImportTTYInterruptionsPreserveState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		sig   os.Signal
	}{
		{"ctrl-c", "\x03", nil}, {"ctrl-d", "\x04", nil}, {"ctrl-z", "\x1a", nil},
		{"sigint", "", syscall.SIGINT}, {"sigterm", "", syscall.SIGTERM}, {"sighup", "", syscall.SIGHUP}, {"sigquit", "", syscall.SIGQUIT}, {"suspend", "", syscall.SIGTSTP},
		{"invalid-control", "\x1b", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newImportTTY(t)
			p.expect(t, "access token (hidden): ")
			p.send(t, "synthetic-partial")
			p.hidden(t)
			if tc.sig != nil {
				if err := p.cmd.Process.Signal(tc.sig); err != nil {
					t.Fatal(err)
				}
			} else {
				p.send(t, tc.input)
			}
			if err := p.cmd.Wait(); err == nil {
				t.Fatal("interrupted import succeeded")
			}
			p.assertPrivate(t)
			p.restored(t)
			if _, err := os.Stat(filepath.Join(p.home, "config.json")); !os.IsNotExist(err) {
				t.Fatal("interruption wrote credentials")
			}
			guard, err := lock.Acquire(filepath.Join(p.home, "config.json.lock"))
			if err != nil {
				t.Fatal("lock not released")
			}
			guard.Close()
		})
	}
}
func TestImportTTYBlocksConcurrentImport(t *testing.T) {
	p := newImportTTY(t)
	p.expect(t, "access token (hidden): ")
	// The running command holds the lock, so a competing real CLI import must
	// stop without displaying a second token prompt or modifying any state.
	cmd := exec.Command(os.Args[0], "-test.run=^TestImportTTYChild$", "--", "config", "import", "--client-id", "synthetic-public", "--scopes", xapi.RequiredScope)
	cmd.Env = p.cmd.Env
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "another sync") || strings.Contains(string(out), "token (hidden)") {
		t.Fatal("concurrent import was not blocked")
	}
	p.send(t, "\x03")
	p.cmd.Wait()
	p.restored(t)
}
func TestImportNoControllingTTYRejectsPipe(t *testing.T) {
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestImportTTYChild$", "--", "config", "import", "--client-id", "synthetic-public", "--scopes", xapi.RequiredScope)
	cmd.Env = append(os.Environ(), "XSTASH_TEST_TTY_CHILD=1", "XSTASH_HOME="+home, "XSTASH_CLIENT_ID=", "XSTASH_CLIENT_SECRET=", "XSTASH_ACCESS_TOKEN=", "XSTASH_REFRESH_TOKEN=")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = strings.NewReader("synthetic-private-access\nsynthetic-private-refresh\n")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "requires an interactive controlling terminal") || strings.Contains(string(out), "synthetic-private") {
		t.Fatal("noninteractive secret input not refused safely")
	}
	if _, err := os.Stat(filepath.Join(home, "config.json")); !os.IsNotExist(err) {
		t.Fatal("pipe input wrote config")
	}
}

func TestImportTTYCancelAtRefreshPrompt(t *testing.T) {
	p := newImportTTY(t)
	p.expect(t, "access token (hidden): ")
	p.send(t, "synthetic-private-access\n")
	p.expect(t, "refresh token (hidden): ")
	p.send(t, "synthetic-partial\x03")
	if err := p.cmd.Wait(); err == nil {
		t.Fatal("cancelled import succeeded")
	}
	p.assertPrivate(t)
	p.restored(t)
	if _, err := os.Stat(filepath.Join(p.home, "config.json")); !os.IsNotExist(err) {
		t.Fatal("partial grant saved")
	}
}
