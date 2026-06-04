package main

import (
	"fmt"
	"io"
	"os/exec"
)

type bashdbBackend struct {
	nodePath    string
	adapterPath string
	bashPath    string
	catPath     string
	mkfifoPath  string
	pkillPath   string
	stdin       io.WriteCloser
	stdout      io.ReadCloser
}

func (b *bashdbBackend) Spawn(port string, stderrWriter io.Writer) (*exec.Cmd, string, error) {
	nodePath := b.nodePath
	if nodePath == "" {
		nodePath = "node"
	}
	adapterPath := b.adapterPath
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

	b.stdin = stdin
	b.stdout = stdout

	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("failed to start bash-debug-adapter: %w (is Node.js and the adapter installed?)", err)
	}

	return cmd, "", nil
}

func (b *bashdbBackend) TransportMode() string {
	return "stdio"
}

func (b *bashdbBackend) AdapterID() string {
	return "bashdb"
}

func (b *bashdbBackend) LaunchArgs(mode, programPath string, stopOnEntry bool, programArgs []string) (map[string]any, error) {
	if mode != "source" && mode != "binary" {
		return nil, fmt.Errorf("unsupported launch mode for bash: %s (use 'source' or 'binary')", mode)
	}

	bashPath := b.bashPath
	if bashPath == "" {
		bashPath = "/bin/bash"
	}
	catPath := b.catPath
	if catPath == "" {
		catPath = "cat"
	}
	mkfifoPath := b.mkfifoPath
	if mkfifoPath == "" {
		mkfifoPath = "mkfifo"
	}
	pkillPath := b.pkillPath
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

func (b *bashdbBackend) CoreArgs(programPath, coreFilePath string) (map[string]any, error) {
	return nil, fmt.Errorf("bash backend does not support core dump debugging")
}

func (b *bashdbBackend) CoreRequestType() string {
	return "launch"
}

func (b *bashdbBackend) AttachArgs(processID int) (map[string]any, error) {
	return nil, fmt.Errorf("bash backend does not support attach mode")
}

func (b *bashdbBackend) RestartArgs(args []string) (map[string]any, error) {
	return nil, nil
}

func (b *bashdbBackend) StdioPipes() (stdout io.ReadCloser, stdin io.WriteCloser) {
	return b.stdout, b.stdin
}
