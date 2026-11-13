// Package execplugin builds the process for an exec plugin and reads back
// its result.
package execplugin

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dewald-L/dock/internal/config"
	"github.com/dewald-L/dock/plugin"
)

// pauseScript runs the program, then waits for Enter so its last screen
// stays readable, and exits with the program's status.
const pauseScript = `"$@"; s=$?; printf '\n\033[2m[dock] press Enter to return\033[0m'; read _; exit $s`

// Paused wraps argv so that, after it exits, the terminal waits for Enter
// before dock redraws. The wrapper exits with argv's status.
func Paused(argv []string) []string {
	return append([]string{"sh", "-c", pauseScript, "dock-exec"}, argv...)
}

// Run is a prepared exec plugin process.
type Run struct {
	Cmd        *exec.Cmd
	ResultFile string
}

// Prepare builds the command. The plugin gets these environment variables:
//
//	DOCK=1                 running inside dock
//	DOCK_PLUGIN_ID         its id
//	DOCK_DATA_DIR          its data directory (exists)
//	DOCK_CONFIG_DIR        its config directory (exists)
//	DOCK_RESULT_FILE       write one line here to show it in dock's status bar
func Prepare(x *config.ExecPlugin) (*Run, error) {
	data, conf, err := config.EnsurePluginDirs(x.ID)
	if err != nil {
		return nil, err
	}
	res, err := os.CreateTemp("", "dock-result-*")
	if err != nil {
		return nil, err
	}
	res.Close()

	argv := x.Command
	if x.Pause {
		argv = Paused(argv)
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = x.Dir
	if c.Dir == "" {
		c.Dir, _ = os.UserHomeDir()
	}
	c.Env = append(os.Environ(),
		"DOCK=1",
		"DOCK_PLUGIN_ID="+x.ID,
		"DOCK_DATA_DIR="+data,
		"DOCK_CONFIG_DIR="+conf,
		"DOCK_RESULT_FILE="+res.Name(),
	)
	return &Run{Cmd: c, ResultFile: res.Name()}, nil
}

// Summary runs the exec plugin's summary command (it must have one) and
// returns the first plugin.MaxSummaryLines lines of its stdout. It gets the
// same environment as the plugin itself, plus DOCK_SUMMARY=1. The process
// is killed when ctx ends.
func Summary(ctx context.Context, x *config.ExecPlugin) ([]string, error) {
	data, conf, err := config.EnsurePluginDirs(x.ID)
	if err != nil {
		return nil, err
	}
	c := exec.CommandContext(ctx, x.Summary[0], x.Summary[1:]...)
	c.Dir = x.Dir
	if c.Dir == "" {
		c.Dir, _ = os.UserHomeDir()
	}
	c.Env = append(os.Environ(),
		"DOCK=1",
		"DOCK_SUMMARY=1",
		"DOCK_PLUGIN_ID="+x.ID,
		"DOCK_DATA_DIR="+data,
		"DOCK_CONFIG_DIR="+conf,
	)
	var out, errOut limitedBuffer
	c.Stdout, c.Stderr = &out, &errOut
	// children that keep stdout open must not hold Wait past the deadline
	c.WaitDelay = 200 * time.Millisecond
	if err := c.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if msg := firstLine(errOut.String()); msg != "" {
			return nil, fmt.Errorf("%s (%v)", msg, err)
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) > plugin.MaxSummaryLines {
		lines = lines[:plugin.MaxSummaryLines]
	}
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	return lines, nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// limitedBuffer keeps the first 16 KiB written to it and drops the rest.
type limitedBuffer struct{ b bytes.Buffer }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := 16<<10 - l.b.Len(); room > 0 {
		l.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string { return l.b.String() }

// Result reads the first non-empty line of the result file and removes it.
func (r *Run) Result() string {
	defer os.Remove(r.ResultFile)
	f, err := os.Open(r.ResultFile)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			if len(line) > 200 {
				line = line[:200]
			}
			return line
		}
	}
	return ""
}
