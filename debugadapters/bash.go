package debugadapters

import (
	"fmt"
	"io"
	"os/exec"
)

// BashBackend implements DebuggerBackend for the vscode-bash-debug adapter.
type BashBackend struct {
	NodePath    string
	AdapterPath string
	BashPath    string
	CatPath     string
	MkfifoPath  string
	PkillPath   string
	Stdin       io.WriteCloser
	Stdout      io.ReadCloser
}

func (b *BashBackend) Spawn(port string, stderrWriter io.Writer) (*exec.Cmd, string, error) {
	nodePath := b.NodePath
	if nodePath == "" {
		nodePath = "node"
	}
	adapterPath := b.AdapterPath
	if adapterPath == "" {
		return nil, "", fmt.Errorf("adapterPath is required (path to bash-debug-adapter's out/bashDebug.js)")
	}

	cmd := exec.Command(nodePath, adapterPath)
	cmd.Stderr = stderrWriter

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	b.Stdin = stdin
	b.Stdout = stdout

	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("failed to start bash-debug-adapter: %w (is Node.js and the adapter installed?)", err)
	}

	return cmd, "", nil
}

func (b *BashBackend) TransportMode() string { return "stdio" }

func (b *BashBackend) AdapterID() string { return "bashdb" }

func (b *BashBackend) LaunchArgs(mode, programPath string, stopOnEntry bool, programArgs []string) (map[string]any, error) {
	if mode != "source" && mode != "binary" {
		return nil, fmt.Errorf("unsupported launch mode for bash: %s (use 'source' or 'binary')", mode)
	}

	bashPath := b.BashPath
	if bashPath == "" {
		bashPath = "/bin/bash"
	}
	catPath := b.CatPath
	if catPath == "" {
		catPath = "cat"
	}
	mkfifoPath := b.MkfifoPath
	if mkfifoPath == "" {
		mkfifoPath = "mkfifo"
	}
	pkillPath := b.PkillPath
	if pkillPath == "" {
		pkillPath = "pkill"
	}

	args := map[string]any{
		"type":         "bashdb",
		"program":      programPath,
		"pathBash":     bashPath,
		"pathCat":      catPath,
		"pathMkfifo":   mkfifoPath,
		"pathPkill":    pkillPath,
		"terminalKind": "debugConsole",
	}
	if stopOnEntry {
		args["stopOnEntry"] = true
	}
	if len(programArgs) > 0 {
		args["args"] = programArgs
	}
	return args, nil
}

func (b *BashBackend) CoreArgs(programPath, coreFilePath string) (map[string]any, error) {
	return nil, fmt.Errorf("bash backend does not support core dump debugging")
}

func (b *BashBackend) CoreRequestType() string { return "launch" }

func (b *BashBackend) AttachArgs(processID int) (map[string]any, error) {
	return nil, fmt.Errorf("bash backend does not support attach mode")
}

func (b *BashBackend) RestartArgs(args []string) (map[string]any, error) {
	return nil, nil
}

func (b *BashBackend) StdioPipes() (stdout io.ReadCloser, stdin io.WriteCloser) {
	return b.Stdout, b.Stdin
}
