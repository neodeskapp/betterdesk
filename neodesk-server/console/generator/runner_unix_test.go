//go:build !windows

package generator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRunnerEndsTheWholeTool kills a tool and the processes it started
// when the timeout passes or the context ends, and returns at once; a
// tool that ends but leaves a process holding its output returns after
// a short wait with its exit code (BUGS RV56: Run waited for every
// child, so the build timeout bounded nothing).
func TestRunnerEndsTheWholeTool(t *testing.T) {
	script := `(sleep 2; touch "$1") & sleep 6`
	for _, c := range []struct {
		name     string
		timeout  time.Duration
		cancel   bool
		timedOut bool
	}{
		{"timeout", 300 * time.Millisecond, false, true},
		{"cancelled", 0, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "child-ran-on")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.cancel {
				time.AfterFunc(300*time.Millisecond, cancel)
			}
			start := time.Now()
			res, err := ExecRunner{}.Run(ctx, Command{Name: "sh", Args: []string{"-c", script, "sh", marker}, Timeout: c.timeout})
			took := time.Since(start)
			if err != nil || res.TimedOut != c.timedOut || res.Code != -1 || took > 1500*time.Millisecond {
				t.Fatalf("got %+v, %v after %v", res, err, took)
			}
			time.Sleep(2500 * time.Millisecond)
			if _, err := os.Stat(marker); err == nil {
				t.Error("a process the tool started ran on")
			}
		})
	}
	t.Run("output held after the end", func(t *testing.T) {
		start := time.Now()
		res, err := ExecRunner{}.Run(context.Background(), Command{Name: "sh", Args: []string{"-c", "sleep 6 & echo done"}})
		if took := time.Since(start); err != nil || res.Code != 0 || res.TimedOut || res.Stdout != "done\n" || took > pipeWait+time.Second {
			t.Fatalf("got %+v, %v after %v", res, err, took)
		}
	})
}
