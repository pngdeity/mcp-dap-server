package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGDBBasic(t *testing.T) {
	requireGDBDeps(t)

	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestCProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()

	f := filepath.Join(ts.cwd, "testdata", "c", "helloworld", "main.c")

	ts.startDebugSessionWithParams(t, map[string]any{
		"debugger": "gdb",
		"mode":     "binary",
		"path":     binaryPath,
		"breakpoints": []map[string]any{
			{"file": f, "line": 11},
		},
	})

	contextStr := ts.getContextContent(t)
	if !strings.Contains(contextStr, "main") {
		t.Errorf("expected 'main' in context, got: %s", contextStr)
	}

	ts.stopDebugger(t)
}

func TestGDBStep(t *testing.T) {
	requireGDBDeps(t)

	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestCProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()

	f := filepath.Join(ts.cwd, "testdata", "c", "helloworld", "main.c")

	ts.startDebugSessionWithParams(t, map[string]any{
		"debugger": "gdb",
		"mode":     "binary",
		"path":     binaryPath,
		"breakpoints": []map[string]any{
			{"file": f, "line": 9},
		},
	})

	text, isErr := ts.callTool(t, "step", map[string]any{"mode": "over"})
	if isErr {
		t.Fatalf("step returned error: %s", text)
	}

	contextStr := ts.getContextContent(t)
	if !strings.Contains(contextStr, "main") {
		t.Errorf("expected to still be in 'main' after step, got: %s", contextStr)
	}

	ts.stopDebugger(t)
}

func TestGDBEvaluate(t *testing.T) {
	requireGDBDeps(t)

	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestCProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()

	f := filepath.Join(ts.cwd, "testdata", "c", "helloworld", "main.c")

	ts.startDebugSessionWithParams(t, map[string]any{
		"debugger": "gdb",
		"mode":     "binary",
		"path":     binaryPath,
		"breakpoints": []map[string]any{
			{"file": f, "line": 12},
		},
	})

	text, isErr := ts.callTool(t, "evaluate", map[string]any{
		"expression": "print x + y",
		"context":    "repl",
	})
	if isErr {
		t.Fatalf("evaluate returned error: %s", text)
	}
	if !strings.Contains(text, "30") {
		t.Errorf("expected evaluation to contain '30', got: %s", text)
	}

	ts.stopDebugger(t)
}

func TestGDBEvaluateWatchContext(t *testing.T) {
	requireGDBDeps(t)

	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestCProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()

	f := filepath.Join(ts.cwd, "testdata", "c", "helloworld", "main.c")

	ts.startDebugSessionWithParams(t, map[string]any{
		"debugger": "gdb",
		"mode":     "binary",
		"path":     binaryPath,
		"breakpoints": []map[string]any{
			{"file": f, "line": 12},
		},
	})

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{"bare expression", "x + y", "30"},
		{"pointer dereference", "*(&x)", "10"},
		{"address-of", "&x", "0x"},
		{"cast expression", "(long)x", "10"},
		{"register access", "$rsp", "0x"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := ts.callTool(t, "evaluate", map[string]any{
				"expression": tc.expression,
			})
			if isErr {
				t.Fatalf("evaluate %q returned error: %s", tc.expression, text)
			}
			if !strings.Contains(text, tc.want) {
				t.Errorf("evaluate %q: expected %q in result, got: %s", tc.expression, tc.want, text)
			}
		})
	}

	ts.stopDebugger(t)
}

func TestGDBFullFlow(t *testing.T) {
	requireGDBDeps(t)

	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestCProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()

	f := filepath.Join(ts.cwd, "testdata", "c", "helloworld", "main.c")

	ts.startDebugSessionWithParams(t, map[string]any{
		"debugger": "gdb",
		"mode":     "binary",
		"path":     binaryPath,
		"breakpoints": []map[string]any{
			{"file": f, "line": 9},
		},
	})

	contextStr := ts.getContextContent(t)
	if !strings.Contains(contextStr, "main") {
		t.Fatalf("expected context to contain 'main', got: %s", contextStr)
	}

	ts.setBreakpointAndContinue(t, f, 12)

	contextStr = ts.getContextContent(t)
	if !strings.Contains(contextStr, "sum") {
		t.Errorf("expected variable 'sum' in context, got: %s", contextStr)
	}

	evalResult, evalErr := ts.callTool(t, "evaluate", map[string]any{"expression": "x + y"})
	if evalErr || !strings.Contains(evalResult, "30") {
		t.Fatalf("evaluate x+y failed: %s (isErr=%v)", evalResult, evalErr)
	}

	evalResult, evalErr = ts.callTool(t, "evaluate", map[string]any{"expression": "*(&sum)"})
	if evalErr || !strings.Contains(evalResult, "30") {
		t.Fatalf("evaluate *(&sum) failed: %s (isErr=%v)", evalResult, evalErr)
	}

	threads, threadsErr := ts.callTool(t, "info", map[string]any{"type": "threads"})
	if threadsErr || !strings.Contains(threads, "Thread") {
		t.Fatalf("info threads failed: %s (isErr=%v)", threads, threadsErr)
	}

	// continue to end — program should terminate
	continueResult, contErr := ts.callTool(t, "continue", map[string]any{})
	if contErr {
		t.Fatalf("continue to end returned error: %s", continueResult)
	}

	ts.stopDebugger(t)
}

func TestGDBCoreDump(t *testing.T) {
	requireGDBDeps(t)

	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestCProgram(t, ts.cwd, "coredump")
	defer cleanupBinary()

	corePath := generateCoreDump(t, binaryPath)
	defer os.Remove(corePath)

	ts.startDebugSessionWithParams(t, map[string]any{
		"debugger":     "gdb",
		"mode":         "core",
		"path":         binaryPath,
		"coreFilePath": corePath,
	})

	contextStr := ts.getContextContent(t)
	for _, want := range []string{"crash", "main"} {
		if !strings.Contains(contextStr, want) {
			t.Errorf("expected stack trace to contain %q, got:\n%s", want, contextStr)
		}
	}

	ts.stopDebugger(t)
}

func TestGDBCoreDumpWithoutPath(t *testing.T) {
	requireGDBDeps(t)

	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestCProgram(t, ts.cwd, "coredump")
	defer cleanupBinary()

	corePath := generateCoreDump(t, binaryPath)
	defer os.Remove(corePath)

	ts.startDebugSessionWithParams(t, map[string]any{
		"debugger":     "gdb",
		"mode":         "core",
		"coreFilePath": corePath,
	})

	contextStr := ts.getContextContent(t)
	for _, want := range []string{"crash", "main"} {
		if !strings.Contains(contextStr, want) {
			t.Errorf("expected stack trace to contain %q, got:\n%s", want, contextStr)
		}
	}

	ts.stopDebugger(t)
}

func TestGDBCoreDumpMissingCoreFile(t *testing.T) {
	requireGDBDeps(t)

	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	text, isErr := ts.callTool(t, "debug", map[string]any{
		"debugger": "gdb",
		"mode":     "core",
	})

	if !isErr {
		t.Error("expected error when coreFilePath is not specified")
	} else if !strings.Contains(text, "coreFilePath is required") {
		t.Errorf("expected 'coreFilePath is required' error, got: %s", text)
	}
}
