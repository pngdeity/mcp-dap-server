package debugadapters

import (
	"strings"
	"testing"
)

func TestGDBBackendLaunchArgs(t *testing.T) {
	t.Parallel()
	backend := &GDBBackend{GDBPath: "gdb"}

	args, err := backend.LaunchArgs("binary", "/path/to/prog", false, []string{"--flag"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if args["program"] != "/path/to/prog" {
		t.Errorf("expected program=/path/to/prog, got: %v", args["program"])
	}
	if args["stopAtBeginningOfMainSubprogram"] != false {
		t.Errorf("expected stopAtBeginningOfMainSubprogram=false, got: %v", args["stopAtBeginningOfMainSubprogram"])
	}
	if _, ok := args["cwd"]; !ok {
		t.Error("expected cwd to be set")
	}
	programArgs, ok := args["args"].([]string)
	if !ok {
		t.Fatalf("expected args to be []string, got: %T", args["args"])
	}
	if len(programArgs) != 1 || programArgs[0] != "--flag" {
		t.Errorf("unexpected args: %v", programArgs)
	}
	if _, ok := args["MIMode"]; ok {
		t.Error("unexpected MIMode key (cpptools artifact)")
	}
	if _, ok := args["miDebuggerPath"]; ok {
		t.Error("unexpected miDebuggerPath key (cpptools artifact)")
	}
}

func TestGDBBackendSourceModeError(t *testing.T) {
	t.Parallel()
	backend := &GDBBackend{GDBPath: "gdb"}

	_, err := backend.LaunchArgs("source", "/path/to/prog", false, nil)
	if err == nil {
		t.Fatal("expected error for source mode with GDB")
	}
	if !strings.Contains(err.Error(), "source") {
		t.Errorf("expected error message to mention 'source', got: %s", err.Error())
	}
}

func TestGDBBackendTransportMode(t *testing.T) {
	t.Parallel()
	backend := &GDBBackend{GDBPath: "gdb"}
	if backend.TransportMode() != "stdio" {
		t.Errorf("expected stdio, got: %s", backend.TransportMode())
	}
}

func TestGDBBackendCoreArgs(t *testing.T) {
	t.Parallel()
	backend := &GDBBackend{GDBPath: "gdb"}
	args, err := backend.CoreArgs("/path/to/program", "/path/to/core")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["program"] != "/path/to/program" {
		t.Errorf("expected program '/path/to/program', got: %v", args["program"])
	}
	if args["coreFile"] != "/path/to/core" {
		t.Errorf("expected coreFile '/path/to/core', got: %v", args["coreFile"])
	}
}

func TestGDBBackendAttachArgs(t *testing.T) {
	t.Parallel()
	backend := &GDBBackend{GDBPath: "gdb"}
	args, err := backend.AttachArgs(12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["pid"] != 12345 {
		t.Errorf("expected pid 12345, got: %v", args["pid"])
	}
}

func TestGDBBackendAdapterID(t *testing.T) {
	t.Parallel()
	backend := &GDBBackend{GDBPath: "gdb"}
	if backend.AdapterID() != "gdb" {
		t.Errorf("expected 'gdb', got: %s", backend.AdapterID())
	}
}
