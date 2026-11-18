//go:build !linux

package servermgmt

// ptyWorks is false: the terminal has a pseudo-terminal only on Linux.
func ptyWorks() bool { return false }

// startPty is not there outside Linux; the terminal uses pipes.
func (s *Service) startPty(int, int, Account) (*shellProc, error) { return nil, errNoPty }
