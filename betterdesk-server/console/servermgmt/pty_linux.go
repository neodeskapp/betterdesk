package servermgmt

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

// ioctl runs one ioctl on f without taking it out of the poller.
func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

// openPty opens a new pseudo-terminal: its main side, unlocked, and the
// path of its other side.
func openPty() (*os.File, string, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", err
	}
	var unlock int32
	if err := ioctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("unlock pty: %w", err)
	}
	var n uint32
	if err := ioctl(master, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("pty number: %w", err)
	}
	return master, "/dev/pts/" + strconv.FormatUint(uint64(n), 10), nil
}

// ptyWorks reports whether a pseudo-terminal can be opened.
func ptyWorks() bool {
	master, _, err := openPty()
	if err != nil {
		return false
	}
	master.Close()
	return true
}

// setSize sets the terminal's size; the shell gets SIGWINCH.
func setSize(f *os.File, cols, rows int) error {
	size := struct{ rows, cols, x, y uint16 }{uint16(rows), uint16(cols), 0, 0}
	return ioctl(f, syscall.TIOCSWINSZ, unsafe.Pointer(&size))
}

// setTermios gives the terminal the settings node-pty gave it: UTF-8
// input, ^C, ^Z and the other control characters, echo, canonical lines,
// 38400 baud.
func setTermios(f *os.File) error {
	var t syscall.Termios
	if err := ioctl(f, syscall.TCGETS, unsafe.Pointer(&t)); err != nil {
		return err
	}
	t.Iflag = syscall.ICRNL | syscall.IXON | syscall.IXANY | syscall.IMAXBEL | syscall.BRKINT | syscall.IUTF8
	t.Oflag = syscall.OPOST | syscall.ONLCR
	t.Cflag = syscall.CREAD | syscall.CS8 | syscall.HUPCL | syscall.B38400
	t.Lflag = syscall.ICANON | syscall.ISIG | syscall.IEXTEN | syscall.ECHO | syscall.ECHOE | syscall.ECHOK | syscall.ECHOKE | syscall.ECHOCTL
	t.Ispeed, t.Ospeed = syscall.B38400, syscall.B38400
	cc := map[int]uint8{
		syscall.VEOF: 4, syscall.VEOL: 0xff, syscall.VEOL2: 0xff, syscall.VERASE: 0x7f, syscall.VWERASE: 23,
		syscall.VKILL: 21, syscall.VREPRINT: 18, syscall.VINTR: 3, syscall.VQUIT: 0x1c, syscall.VSUSP: 26,
		syscall.VSTART: 17, syscall.VSTOP: 19, syscall.VLNEXT: 22, syscall.VDISCARD: 15, syscall.VMIN: 1, syscall.VTIME: 0,
	}
	for i, v := range cc {
		t.Cc[i] = v
	}
	return ioctl(f, syscall.TCSETS, unsafe.Pointer(&t))
}

// startPty starts the shell on a new pseudo-terminal of cols by rows, as
// the leader of a new session with the terminal as its controlling one.
func (s *Service) startPty(cols, rows int, a Account) (*shellProc, error) {
	cred, err := s.credential(a)
	if err != nil {
		return nil, err
	}
	master, name, err := openPty()
	if err != nil {
		return nil, err
	}
	slave, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, err
	}
	if err := setTermios(slave); err != nil {
		slave.Close()
		master.Close()
		return nil, err
	}
	if err := setSize(master, cols, rows); err != nil {
		slave.Close()
		master.Close()
		return nil, err
	}
	shell := firstSet(a.Shell, s.lastResortShell())
	cmd := &exec.Cmd{
		Path: shell, Args: []string{shell}, Dir: firstSet(a.Home, s.home()), Env: s.shellEnv(a, cols, rows, true),
		Stdin: slave, Stdout: slave, Stderr: slave,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0, Credential: cred},
	}
	err = cmd.Start()
	slave.Close()
	if err != nil {
		master.Close()
		return nil, err
	}
	p := newShellProc("pty", cmd, master, []*os.File{master})
	p.resizeTo = func(cols, rows int) { _ = setSize(master, cols, rows) }
	return p, nil
}
