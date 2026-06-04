package main

import (
	"io"
	"os/exec"
	"strings"
	"testing"
)

func testBackendCompliance(t *testing.T, factory func() DebuggerBackend) {
	t.Helper()

	t.Run("TransportMode", func(t *testing.T) {
		mode := factory().TransportMode()
		if mode != "tcp" && mode != "stdio" {
			t.Errorf("expected 'tcp' or 'stdio', got %q", mode)
		}
	})

	t.Run("AdapterID", func(t *testing.T) {
		id := factory().AdapterID()
		if id == "" {
			t.Error("expected non-empty adapter ID")
		}
	})

	t.Run("CoreRequestType", func(t *testing.T) {
		typ := factory().CoreRequestType()
		if typ != "launch" && typ != "attach" {
			t.Errorf("expected 'launch' or 'attach', got %q", typ)
		}
	})

	t.Run("LaunchArgs_binary", func(t *testing.T) {
		args, err := factory().LaunchArgs("binary", "/tmp/test/prog", true, nil)
		if err != nil {
			if args != nil {
				t.Errorf("error non-nil but args also non-nil: %v", args)
			}
			t.Logf("binary mode skipped (expected error): %v", err)
			return
		}
		if args == nil {
			t.Error("expected non-nil args when err is nil")
		}
	})

	t.Run("LaunchArgs_source", func(t *testing.T) {
		args, err := factory().LaunchArgs("source", "/tmp/test/prog", false, nil)
		if err != nil {
			if args != nil {
				t.Errorf("error non-nil but args also non-nil: %v", args)
			}
			t.Logf("source mode skipped (expected error): %v", err)
			return
		}
		if args == nil {
			t.Error("expected non-nil args when err is nil")
		}
	})

	t.Run("CoreArgs", func(t *testing.T) {
		args, err := factory().CoreArgs("/tmp/prog", "/tmp/core")
		if err != nil {
			if args != nil {
				t.Errorf("error non-nil but args also non-nil: %v", args)
			}
			t.Logf("core args skipped (expected error): %v", err)
			return
		}
		if args == nil {
			t.Error("expected non-nil args when err is nil")
		}
	})

	t.Run("AttachArgs", func(t *testing.T) {
		args, err := factory().AttachArgs(12345)
		if err != nil {
			if args != nil {
				t.Errorf("error non-nil but args also non-nil: %v", args)
			}
			t.Logf("attach args skipped (expected error): %v", err)
			return
		}
		if args == nil {
			t.Error("expected non-nil args when err is nil")
		}
	})

	t.Run("RestartArgs", func(t *testing.T) {
		args, err := factory().RestartArgs([]string{"--flag"})
		if err != nil {
			if args != nil {
				t.Errorf("error non-nil but args also non-nil: %v", args)
			}
			return
		}
		if args != nil {
			if _, ok := args["arguments"]; ok {
				t.Log("restart args: launches with custom args")
			}
		}
	})

	t.Run("StdioPipes", func(t *testing.T) {
		r, w := factory().StdioPipes()
		if (r == nil) != (w == nil) {
			t.Error("expected both pipes nil or both non-nil")
		}
		if r != nil {
			t.Log("stdio backend: pipes captured from spawn")
		} else {
			t.Log("tcp backend: no stdio pipes")
		}
	})
}

func TestDelveInterfaceCompliance(t *testing.T) {
	testBackendCompliance(t, func() DebuggerBackend { return &delveBackend{} })
}

func TestGDBInterfaceCompliance(t *testing.T) {
	testBackendCompliance(t, func() DebuggerBackend {
		return &gdbBackend{gdbPath: "gdb"}
	})
}

func TestDelveBackendSpawn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("dlv not found in PATH")
	}

	backend := &delveBackend{}
	cmd, listenAddr, err := backend.Spawn(":0", io.Discard)
	if err != nil {
		t.Fatalf("failed to spawn delve: %v", err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	if listenAddr == "" {
		t.Error("expected non-empty listen address")
	}
	if backend.TransportMode() != "tcp" {
		t.Errorf("expected tcp transport, got: %s", backend.TransportMode())
	}
	t.Logf("Delve listening at: %s", listenAddr)
}

func TestDelveBackendLaunchArgs(t *testing.T) {
	backend := &delveBackend{}

	t.Run("source mode", func(t *testing.T) {
		args, err := backend.LaunchArgs("source", "/path/to/main.go", true, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if args["mode"] != "debug" {
			t.Errorf("expected mode 'debug', got: %v", args["mode"])
		}
		if args["program"] != "/path/to/main.go" {
			t.Errorf("expected program '/path/to/main.go', got: %v", args["program"])
		}
		if args["stopOnEntry"] != true {
			t.Errorf("expected stopOnEntry true, got: %v", args["stopOnEntry"])
		}
		if _, ok := args["args"]; ok {
			t.Error("expected no args key when programArgs is nil")
		}
	})

	t.Run("binary mode", func(t *testing.T) {
		args, err := backend.LaunchArgs("binary", "/path/to/binary", false, []string{"--flag", "value"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if args["mode"] != "exec" {
			t.Errorf("expected mode 'exec', got: %v", args["mode"])
		}
		if args["program"] != "/path/to/binary" {
			t.Errorf("expected program '/path/to/binary', got: %v", args["program"])
		}
		if args["stopOnEntry"] != false {
			t.Errorf("expected stopOnEntry false, got: %v", args["stopOnEntry"])
		}
		programArgs, ok := args["args"].([]string)
		if !ok {
			t.Fatalf("expected args to be []string, got: %T", args["args"])
		}
		if len(programArgs) != 2 || programArgs[0] != "--flag" || programArgs[1] != "value" {
			t.Errorf("unexpected args: %v", programArgs)
		}
	})

	t.Run("unsupported mode", func(t *testing.T) {
		_, err := backend.LaunchArgs("invalid", "/path", false, nil)
		if err == nil {
			t.Error("expected error for unsupported mode")
		}
	})
}

func TestDelveBackendCoreArgs(t *testing.T) {
	backend := &delveBackend{}
	args, err := backend.CoreArgs("/path/to/program", "/path/to/core")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["mode"] != "core" {
		t.Errorf("expected mode 'core', got: %v", args["mode"])
	}
	if args["program"] != "/path/to/program" {
		t.Errorf("expected program '/path/to/program', got: %v", args["program"])
	}
	if args["coreFilePath"] != "/path/to/core" {
		t.Errorf("expected coreFilePath '/path/to/core', got: %v", args["coreFilePath"])
	}
}

func TestDelveBackendAttachArgs(t *testing.T) {
	backend := &delveBackend{}
	args, err := backend.AttachArgs(12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["mode"] != "local" {
		t.Errorf("expected mode 'local', got: %v", args["mode"])
	}
	if args["processId"] != 12345 {
		t.Errorf("expected processId 12345, got: %v", args["processId"])
	}
}

func TestGDBBackendLaunchArgs(t *testing.T) {
	backend := &gdbBackend{gdbPath: "gdb"}

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
	// Verify cpptools-specific keys are NOT present
	if _, ok := args["MIMode"]; ok {
		t.Error("unexpected MIMode key (cpptools artifact)")
	}
	if _, ok := args["miDebuggerPath"]; ok {
		t.Error("unexpected miDebuggerPath key (cpptools artifact)")
	}
}

func TestGDBBackendSourceModeError(t *testing.T) {
	backend := &gdbBackend{gdbPath: "gdb"}

	_, err := backend.LaunchArgs("source", "/path/to/prog", false, nil)
	if err == nil {
		t.Fatal("expected error for source mode with GDB")
	}
	if !strings.Contains(err.Error(), "source") {
		t.Errorf("expected error message to mention 'source', got: %s", err.Error())
	}
}

func TestGDBBackendTransportMode(t *testing.T) {
	backend := &gdbBackend{gdbPath: "gdb"}
	if backend.TransportMode() != "stdio" {
		t.Errorf("expected stdio, got: %s", backend.TransportMode())
	}
}

func TestGDBBackendCoreArgs(t *testing.T) {
	backend := &gdbBackend{gdbPath: "gdb"}
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
	backend := &gdbBackend{gdbPath: "gdb"}
	args, err := backend.AttachArgs(12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["pid"] != 12345 {
		t.Errorf("expected pid 12345, got: %v", args["pid"])
	}
}

func TestGDBBackendAdapterID(t *testing.T) {
	backend := &gdbBackend{gdbPath: "gdb"}
	if backend.AdapterID() != "gdb" {
		t.Errorf("expected 'gdb', got: %s", backend.AdapterID())
	}
}

func TestGDBBackendSpawn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("gdb"); err != nil {
		t.Skip("gdb not found in PATH")
	}

	backend := &gdbBackend{gdbPath: "gdb"}
	cmd, listenAddr, err := backend.Spawn(":0", io.Discard)
	if err != nil {
		t.Fatalf("failed to spawn gdb: %v", err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	if listenAddr != "" {
		t.Errorf("expected empty listen address for stdio transport, got: %s", listenAddr)
	}
	if backend.TransportMode() != "stdio" {
		t.Errorf("expected stdio transport, got: %s", backend.TransportMode())
	}
}
