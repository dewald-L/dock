// Package execplugin builds the process for an exec plugin and reads back
// its result.
package execplugin

import (
	"bufio"
	"os"
	"os/exec"
	"strings"

	"github.com/dewald-L/dock/internal/config"
)

// pauseScript runs the program, then waits for Enter so its last screen
// stays readable, and exits with the program's status.
const pauseScript = `"$@"; s=$?; printf '\n\033[2m[dock] press Enter to return\033[0m'; read _; exit $s`

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
		argv = append([]string{"sh", "-c", pauseScript, "dock-exec"}, argv...)
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
