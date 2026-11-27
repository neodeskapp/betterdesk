package logging

import (
	"os"
	"path/filepath"
	"sync"
)

// RotatingFile is a log file that moves to NAME.1 once it would grow past
// its limit, replacing an older NAME.1, and starts again empty. It is the
// log of a process that nothing else writes a log for, such as the
// Windows service, which has no standard error.
type RotatingFile struct {
	mu    sync.Mutex
	path  string
	limit int64
	file  *os.File
	// written counts the bytes towards the next move: the file's size,
	// or after a move that failed the bytes written since.
	written int64
	onOpen  func(*os.File)
}

// OpenRotatingFile opens the log at path for appending, with its folder.
// A file that is already at the limit moves to NAME.1 first. onOpen, when
// not nil, gets every file the log opens before anything is written to
// it, and nil just before that file is closed.
func OpenRotatingFile(path string, limit int64, onOpen func(*os.File)) (*RotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	r := &RotatingFile{path: path, limit: limit, onOpen: onOpen}
	if err := r.open(); err != nil {
		return nil, err
	}
	if r.written >= limit {
		_ = r.rotate()
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.file, r.written = f, info.Size()
	if r.onOpen != nil {
		r.onOpen(f)
	}
	return nil
}

// Write appends p, moving the file aside first when p would take it past
// the limit. A line longer than the limit still goes in whole.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return 0, os.ErrClosed
	}
	if r.written > 0 && r.written+int64(len(p)) > r.limit {
		_ = r.rotate()
		if r.file == nil {
			return 0, os.ErrClosed
		}
	}
	n, err := r.file.Write(p)
	r.written += int64(n)
	return n, err
}

// rotate closes the file, moves it to NAME.1 and opens NAME again. When
// the move fails (another program holds the file without letting it be
// renamed), the log goes on in the same file and tries again after
// another limit's worth of bytes.
func (r *RotatingFile) rotate() error {
	if r.onOpen != nil {
		r.onOpen(nil)
	}
	r.file.Close()
	r.file = nil
	moveErr := os.Rename(r.path, r.path+".1")
	if err := r.open(); err != nil {
		return err
	}
	if moveErr != nil {
		r.written = 0
	}
	return moveErr
}

// Close closes the file; later writes fail.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	if r.onOpen != nil {
		r.onOpen(nil)
	}
	err := r.file.Close()
	r.file = nil
	return err
}
