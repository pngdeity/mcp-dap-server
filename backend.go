package main

import (
	"fmt"
	"os/exec"

	"github.com/pngdeity/mcp-dap-server/debugadapters"
)

func newBackend(params DebugParams) (debugadapters.DebuggerBackend, error) {
	debugger := params.Debugger
	if debugger == "" {
		if params.Language != "" {
			debugger = debugadapters.DefaultDebuggerFor(params.Language)
		}
		if debugger == "" {
			debugger = "delve"
		}
	}

	switch debugger {
	case "delve":
		return &debugadapters.DelveBackend{}, nil
	case "bash":
		return &debugadapters.BashBackend{
			NodePath:    params.BashNodePath,
			AdapterPath: params.BashAdapterPath,
			BashPath:    params.BashBashPath,
			CatPath:     params.BashCatPath,
			MkfifoPath:  params.BashMkfifoPath,
			PkillPath:   params.BashPkillPath,
		}, nil
	case "gdb":
		gdbPath := params.GDBPath
		if gdbPath == "" {
			var err error
			gdbPath, err = exec.LookPath("gdb")
			if err != nil {
				return nil, fmt.Errorf("GDB not found in PATH. Install GDB 14+ or set the gdbPath parameter")
			}
		}
		return &debugadapters.GDBBackend{GDBPath: gdbPath, ToolLogPath: params.ToolLog}, nil
	default:
		return nil, fmt.Errorf("unsupported debugger: %s (must be 'delve', 'gdb', or 'bash')", debugger)
	}
}
