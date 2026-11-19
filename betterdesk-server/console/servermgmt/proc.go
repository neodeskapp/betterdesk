package servermgmt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// drainWait is how long the output of a shell that ended is still read:
// something it started in the background may keep the output open.
const drainWait = 200 * time.Millisecond

// shellProc is a running shell: its process, where its input goes and its
// output comes from.
type shellProc struct {
	// kind is "pty" for a pseudo-terminal, "pipe" for pipes
	kind string
	cmd  *exec.Cmd
	// input is the terminal's main side or the write end of standard input
	input io.WriteCloser
	// outputs are the terminal's main side, or the read ends of standard
	// output and standard error
	outputs []*os.File
	// resizeTo sets the terminal's size; nil for pipes
	resizeTo func(cols, rows int)

	mu      sync.Mutex
	queue   []string
	wake    chan struct{}
	stopped bool

	// exited is closed when the process has ended
	exited chan struct{}
	// ended is closed when the process has ended and its output is read
	ended    chan struct{}
	killOnce sync.Once
}

func newShellProc(kind string, cmd *exec.Cmd, input io.WriteCloser, outputs []*os.File) *shellProc {
	return &shellProc{
		kind: kind, cmd: cmd, input: input, outputs: outputs,
		wake: make(chan struct{}, 1), exited: make(chan struct{}), ended: make(chan struct{}),
	}
}

// startShell starts the account's shell for a terminal of cols by rows:
// on a pseudo-terminal when there is one, else with pipes.
func (s *Service) startShell(cols, rows int, a Account) (*shellProc, error) {
	if s.PtyAvailable() {
		if p, err := s.startPty(cols, rows, a); err == nil {
			return p, nil
		}
	}
	p, err := s.startPipe(cols, rows, a)
	if err != nil {
		return nil, fmt.Errorf("start shell %s: %w", a.Shell, err)
	}
	return p, nil
}

// start reads the output and hands it to out, writes the queued input,
// and when the process has ended and its output is read calls exit with
// the code and the signal, in the form of the shell's kind.
func (p *shellProc) start(out func(string), exit func(code, signal any)) {
	var readers sync.WaitGroup
	for _, f := range p.outputs {
		readers.Add(1)
		go func(f *os.File) {
			defer readers.Done()
			p.read(f, out)
		}(f)
	}
	go p.writeLoop()
	go func() {
		err := p.cmd.Wait()
		close(p.exited)
		drained := make(chan struct{})
		go func() {
			readers.Wait()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(drainWait):
		}
		for _, f := range p.outputs {
			f.Close()
		}
		<-drained
		p.stopInput()
		code, signal := exitStatus(p.kind, p.cmd, err)
		exit(code, signal)
		close(p.ended)
	}()
}

// read hands the output of one source to out until it ends, decoded
// across reads: a character split between two reads goes out whole.
func (p *shellProc) read(f *os.File, out func(string)) {
	buf := make([]byte, 64*1024)
	var carry []byte
	for {
		n, err := f.Read(buf)
		if n > 0 {
			var text string
			text, carry = decodeStream(append(carry, buf[:n]...))
			if text != "" {
				out(text)
			}
		}
		if err != nil {
			return
		}
	}
}

// decodeStream decodes UTF-8 up to a sequence that the next read may
// finish, which it returns to go in front of that read.
func decodeStream(b []byte) (string, []byte) {
	keep := incompleteTail(b)
	return decodeUTF8(b[:len(b)-keep]), append([]byte(nil), b[len(b)-keep:]...)
}

// incompleteTail is the length of a UTF-8 sequence at the end of b that
// has begun well but lacks bytes.
func incompleteTail(b []byte) int {
	for back := 1; back <= 3 && back <= len(b); back++ {
		c := b[len(b)-back]
		if c&0xC0 == 0x80 {
			continue
		}
		need := 0
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 2
		case c >= 0xE0 && c <= 0xEF:
			need = 3
		case c >= 0xF0 && c <= 0xF4:
			need = 4
		}
		if need > back && decodeUTF8(b[len(b)-back:]) == "�" {
			return back
		}
		return 0
	}
	return 0
}

// write queues input for the shell. Nothing waits for the shell to read
// it.
func (p *shellProc) write(text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.queue = append(p.queue, text)
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *shellProc) writeLoop() {
	for range p.wake {
		for {
			p.mu.Lock()
			if p.stopped || len(p.queue) == 0 {
				p.mu.Unlock()
				break
			}
			text := p.queue[0]
			p.queue = p.queue[1:]
			p.mu.Unlock()
			if _, err := io.WriteString(p.input, text); err != nil {
				p.stopInput()
				break
			}
		}
		p.mu.Lock()
		stopped := p.stopped
		p.mu.Unlock()
		if stopped {
			return
		}
	}
}

// stopInput drops what is queued, closes the input and ends the writer.
func (p *shellProc) stopInput() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.stopped = true
	p.queue = nil
	p.input.Close()
	close(p.wake)
}

// resize sets the terminal's size; pipes have none.
func (p *shellProc) resize(cols, rows int) {
	if p.resizeTo != nil {
		p.resizeTo(cols, rows)
	}
}

// kill asks the shell to end: a hang-up for a terminal, a termination for
// pipes, to it and its process group, and the end of its input (an
// interactive shell ignores the termination but stops at the end of its
// input). When it has not ended after wait, it and its group are killed.
func (p *shellProc) kill(wait time.Duration) {
	p.killOnce.Do(func() {
		select {
		case <-p.exited:
			return
		default:
		}
		signalShell(p, false)
		p.stopInput()
		go func() {
			select {
			case <-p.exited:
			case <-time.After(wait):
				signalShell(p, true)
			}
		}()
	})
}

// errNoPty is a machine without pseudo-terminals.
var errNoPty = errors.New("no pseudo-terminal on this system")
