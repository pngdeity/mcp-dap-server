package debugadapters

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// DebuggerBackend abstracts the debugger-specific logic for spawning a DAP
// server and building the launch/attach argument maps. Each supported debugger
// (Delve, GDB via native DAP, etc.) implements this interface.
type DebuggerBackend interface {
	Spawn(port string, stderrWriter io.Writer) (cmd *exec.Cmd, listenAddr string, err error)
	TransportMode() string
	AdapterID() string
	LaunchArgs(mode, programPath string, stopOnEntry bool, programArgs []string) (map[string]any, error)
	CoreArgs(programPath, coreFilePath string) (map[string]any, error)
	CoreRequestType() string
	AttachArgs(processID int) (map[string]any, error)
	RestartArgs(args []string) (map[string]any, error)
	StdioPipes() (stdout io.ReadCloser, stdin io.WriteCloser)
}

// DelveBackend implements DebuggerBackend for the Delve debugger (Go).
type DelveBackend struct{}

func (b *DelveBackend) Spawn(port string, stderrWriter io.Writer) (*exec.Cmd, string, error) {
	cmd := exec.Command("dlv", "dap", "--listen", port, "--log", "--log-output", "dap")
	cmd.Stderr = stderrWriter
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}

	r := bufio.NewReader(stdout)
	var listenAddr string
	for {
		s, err := r.ReadString('\n')
		if err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			return nil, "", err
		}
		if strings.HasPrefix(s, "DAP server listening at") {
			parts := strings.SplitN(s, ": ", 2)
			if len(parts) == 2 {
				listenAddr = strings.TrimSpace(parts[1])
			}
			break
		}
	}
	if listenAddr == "" {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, "", fmt.Errorf("failed to parse DAP server listen address")
	}

	return cmd, listenAddr, nil
}

func (b *DelveBackend) TransportMode() string { return "tcp" }

func (b *DelveBackend) AdapterID() string { return "go" }

func (b *DelveBackend) LaunchArgs(mode, programPath string, stopOnEntry bool, programArgs []string) (map[string]any, error) {
	dlvMode := mode
	switch mode {
	case "source":
		dlvMode = "debug"
	case "binary":
		dlvMode = "exec"
	default:
		return nil, fmt.Errorf("unsupported launch mode for delve: %s", mode)
	}

	args := map[string]any{
		"request":     "launch",
		"mode":        dlvMode,
		"program":     programPath,
		"stopOnEntry": stopOnEntry,
	}
	if len(programArgs) > 0 {
		args["args"] = programArgs
	}
	return args, nil
}

func (b *DelveBackend) CoreRequestType() string { return "launch" }

func (b *DelveBackend) CoreArgs(programPath, coreFilePath string) (map[string]any, error) {
	return map[string]any{
		"request":      "launch",
		"mode":         "core",
		"program":      programPath,
		"coreFilePath": coreFilePath,
	}, nil
}

func (b *DelveBackend) AttachArgs(processID int) (map[string]any, error) {
	return map[string]any{
		"request":   "attach",
		"mode":      "local",
		"processId": processID,
	}, nil
}

func (b *DelveBackend) RestartArgs(args []string) (map[string]any, error) {
	return map[string]any{
		"arguments": map[string]any{
			"request":     "launch",
			"mode":        "exec",
			"stopOnEntry": false,
			"args":        args,
			"rebuild":     false,
		},
	}, nil
}

func (b *DelveBackend) StdioPipes() (io.ReadCloser, io.WriteCloser) {
	return nil, nil
}

// GDBBackend implements DebuggerBackend for GDB's native DAP server.
// Requires GDB 14+. Communicates over stdio.
type GDBBackend struct {
	GDBPath     string
	ToolLogPath string
	Stdin       io.WriteCloser
	Stdout      io.ReadCloser
}

func (g *GDBBackend) Spawn(port string, stderrWriter io.Writer) (*exec.Cmd, string, error) {
	gdbPath := g.GDBPath
	if gdbPath == "" {
		gdbPath = "gdb"
	}
	args := []string{"-i", "dap"}
	if g.ToolLogPath != "" {
		args = append([]string{"-iex", "set debug dap-log-file " + g.ToolLogPath}, args...)
	}
	cmd := exec.Command(gdbPath, args...)
	cmd.Stderr = stderrWriter

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	g.Stdin = stdin
	g.Stdout = stdout

	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("failed to start gdb: %w (is GDB 14+ installed?)", err)
	}

	return cmd, "", nil
}

func (g *GDBBackend) TransportMode() string { return "stdio" }

func (g *GDBBackend) AdapterID() string { return "gdb" }

func (g *GDBBackend) RestartArgs(args []string) (map[string]any, error) {
	return nil, nil
}

func (g *GDBBackend) StdioPipes() (stdout io.ReadCloser, stdin io.WriteCloser) {
	return g.Stdout, g.Stdin
}

func (g *GDBBackend) LaunchArgs(mode, programPath string, stopOnEntry bool, programArgs []string) (map[string]any, error) {
	if mode == "source" {
		return nil, fmt.Errorf("GDB does not support 'source' mode. Compile your program with debug symbols (gcc -g -O0) and use 'binary' mode instead")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("unable to get working directory for GDB launch: %w", err)
	}
	args := map[string]any{
		"program":                         programPath,
		"cwd":                             cwd,
		"stopAtBeginningOfMainSubprogram": stopOnEntry,
	}
	if len(programArgs) > 0 {
		args["args"] = programArgs
	}
	return args, nil
}

func (g *GDBBackend) CoreRequestType() string { return "attach" }

func (g *GDBBackend) CoreArgs(programPath, coreFilePath string) (map[string]any, error) {
	args := map[string]any{
		"coreFile": coreFilePath,
	}
	if programPath != "" {
		args["program"] = programPath
	}
	return args, nil
}

func (g *GDBBackend) AttachArgs(processID int) (map[string]any, error) {
	return map[string]any{
		"pid": processID,
	}, nil
}

// DefaultDebuggerFor maps a language string to the default debugger backend name.
func DefaultDebuggerFor(language string) string {
	switch language {
	case "go":
		return "delve"
	case "c", "cpp", "c++":
		return "gdb"
	case "bash", "sh":
		return "bash"
	}
	return ""
}
