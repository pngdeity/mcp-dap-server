//go:build integration

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBasic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil)

	f := filepath.Join(ts.cwd, "testdata", "go", "helloworld", "main.go")
	ts.setBreakpointAndContinue(t, f, 7)

	contextStr := ts.getContextContent(t)
	if !strings.Contains(contextStr, "main.main") {
		t.Errorf("expected 'main.main' in context, got: %s", contextStr)
	}
	if !strings.Contains(contextStr, "main.go") {
		t.Errorf("expected 'main.go' in context, got: %s", contextStr)
	}

	evalResult, evalErr := ts.callTool(t, "evaluate", map[string]any{
		"expression": "greeting",
		"frameId":    1000,
		"context":    "repl",
	})
	if evalErr {
		t.Fatalf("evaluate returned error: %s", evalResult)
	}
	if !strings.Contains(evalResult, "hello, world") {
		t.Errorf("expected 'hello, world' in evaluate result, got: %s", evalResult)
	}

	ts.stopDebugger(t)
}

func TestRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "restart")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil, "world")

	f := filepath.Join(ts.cwd, "testdata", "go", "restart", "main.go")
	ts.setBreakpointAndContinue(t, f, 15)

	restartResult, isErr := ts.callTool(t, "restart", map[string]any{
		"args": []string{"me, its me again"},
	})
	if isErr {
		t.Fatalf("restart returned error: %s", restartResult)
	}

	continueResult, contErr := ts.callTool(t, "continue", map[string]any{})
	if contErr {
		t.Fatalf("continue after restart returned error: %s", continueResult)
	}

	contextStr := ts.getContextContent(t)
	if !strings.Contains(contextStr, "main.go:15") {
		t.Errorf("expected breakpoint at main.go:15 after restart, got: %s", contextStr)
	}

	evalResult, evalErr := ts.callTool(t, "evaluate", map[string]any{
		"expression": "greeting",
		"frameId":    1000,
		"context":    "repl",
	})
	if evalErr {
		t.Fatalf("evaluate after restart returned error: %s", evalResult)
	}
	if !strings.Contains(evalResult, "hello me, its me again") {
		t.Errorf("expected 'hello me, its me again', got: %s", evalResult)
	}

	ts.stopDebugger(t)
}

func TestContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil)

	f := filepath.Join(ts.cwd, "testdata", "go", "helloworld", "main.go")
	ts.setBreakpointAndContinue(t, f, 7)

	contextStr := ts.getContextContent(t)
	t.Logf("Context output:\n%s", contextStr)

	for _, want := range []string{"main.main", "main.go:7", "Locals", "greeting", `"hello, world"`} {
		if !strings.Contains(contextStr, want) {
			t.Errorf("expected context to contain %q, got:\n%s", want, contextStr)
		}
	}

	ts.stopDebugger(t)
}

func TestVariables(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "scopes")
	defer cleanupBinary()

	f := filepath.Join(ts.cwd, "testdata", "go", "scopes", "main.go")
	ts.startDebugSession(t, "0", binaryPath, []map[string]any{{"file": f, "line": 67}})

	contextStr := ts.getContextContent(t)
	t.Logf("Context in processCollection:\n%s", contextStr)

	for _, want := range []string{"processCollection", "nums", "len: 5", "dict", "sum", "= 15", "count", "= 3"} {
		if !strings.Contains(contextStr, want) {
			t.Errorf("expected %q in context, got:\n%s", want, contextStr)
		}
	}

	ts.stopDebugger(t)
}

func TestStep(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "step")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil)

	f := filepath.Join(ts.cwd, "testdata", "go", "step", "main.go")
	ts.setBreakpointAndContinue(t, f, 7)

	steps := []int{10, 13, 16}
	for _, line := range steps {
		text, isErr := ts.callTool(t, "step", map[string]any{"mode": "over", "threadId": 1})
		if isErr {
			t.Fatalf("step to line %d returned error: %s", line, text)
		}
	}

	contextStr := ts.getContextContent(t)
	for _, check := range []string{"x (int) = 10", "y (int) = 20", "sum (int) = 30"} {
		if !strings.Contains(contextStr, check) {
			t.Errorf("expected %q in context, got:\n%s", check, contextStr)
		}
	}

	ts.stopDebugger(t)
}

func TestStepIn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "step")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil)

	f := filepath.Join(ts.cwd, "testdata", "go", "step", "main.go")
	ts.setBreakpointAndContinue(t, f, 16)

	text, isErr := ts.callTool(t, "step", map[string]any{
		"mode":        "in",
		"threadId":    1,
		"fullContext": true,
	})
	if isErr {
		t.Fatalf("step in returned error: %s", text)
	}
	if !strings.Contains(text, "Function: fmt.Sprintf") {
		t.Errorf("expected to be in fmt.Sprintf after step in, got:\n%s", text)
	}

	ts.stopDebugger(t)
}

func TestStepOut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "step")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil)

	f := filepath.Join(ts.cwd, "testdata", "go", "step", "main.go")
	ts.setBreakpointAndContinue(t, f, 16)

	_, isErr := ts.callTool(t, "step", map[string]any{"mode": "in", "threadId": 1})
	if isErr {
		t.Fatal("step in failed")
	}

	text, isErr := ts.callTool(t, "step", map[string]any{"mode": "out", "threadId": 1})
	if isErr {
		t.Fatalf("step out returned error: %s", text)
	}
	if !strings.Contains(text, "main.main") {
		t.Errorf("expected to be back in main.main after step out, got: %s", text)
	}

	ts.stopDebugger(t)
}

func TestSetVariable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "step")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil)

	f := filepath.Join(ts.cwd, "testdata", "go", "step", "main.go")
	ts.setBreakpointAndContinue(t, f, 13)

	contextStr := ts.getContextContent(t)
	if !strings.Contains(contextStr, "x (int) = 10") {
		t.Fatalf("expected x=10, got:\n%s", contextStr)
	}

	text, isErr := ts.callTool(t, "set-variable", map[string]any{
		"variablesReference": 1001,
		"name":               "x",
		"value":              "99",
	})
	if isErr {
		t.Fatalf("set-variable returned error: %s", text)
	}

	evalText, isErr := ts.callTool(t, "evaluate", map[string]any{"expression": "x", "context": "repl"})
	if isErr {
		t.Fatalf("evaluate returned error: %s", evalText)
	}
	if !strings.Contains(evalText, "99") {
		t.Errorf("expected x=99 after set-variable, got: %s", evalText)
	}

	ts.stopDebugger(t)
}

func TestPause(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil)

	f := filepath.Join(ts.cwd, "testdata", "go", "helloworld", "main.go")
	ts.setBreakpointAndContinue(t, f, 7)

	text, isErr := ts.callTool(t, "pause", map[string]any{"threadId": 1})
	if isErr {
		t.Fatalf("pause returned error: %s", text)
	}
	if !strings.Contains(text, "Paused") {
		t.Errorf("expected 'Paused' message, got: %s", text)
	}

	ts.stopDebugger(t)
}

func TestCoreDump(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "coredump")
	defer cleanupBinary()

	corePath := generateCoreDump(t, binaryPath)
	defer os.Remove(corePath)

	text, isErr := ts.callTool(t, "debug", map[string]any{
		"mode":         "core",
		"path":         binaryPath,
		"coreFilePath": corePath,
		"port":         "9095",
	})
	if isErr {
		t.Fatalf("core debug session returned error: %s", text)
	}

	contextStr := ts.getContextContent(t)
	if !strings.Contains(contextStr, "main.") {
		t.Errorf("expected stack trace to contain 'main.', got:\n%s", contextStr)
	}

	ts.stopDebugger(t)
}

func TestClearBreakpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil)

	f := filepath.Join(ts.cwd, "testdata", "go", "helloworld", "main.go")

	_, isErr := ts.callTool(t, "breakpoint", map[string]any{"file": f, "line": 7})
	if isErr {
		t.Fatal("failed to set breakpoint")
	}

	text, isErr := ts.callTool(t, "clear-breakpoints", map[string]any{"file": f})
	if isErr || !strings.Contains(text, "Cleared breakpoints in") {
		t.Fatalf("clear-breakpoints file failed: %s (isErr=%v)", text, isErr)
	}

	text, isErr = ts.callTool(t, "clear-breakpoints", map[string]any{"all": true})
	if isErr || !strings.Contains(text, "Cleared all breakpoints") {
		t.Fatalf("clear-breakpoints all failed: %s (isErr=%v)", text, isErr)
	}

	// No args: expect error
	result, err := ts.session.CallTool(ts.ctx, &mcp.CallToolParams{
		Name: "clear-breakpoints", Arguments: map[string]any{},
	})
	if err == nil && !result.IsError {
		t.Error("expected error when neither file nor all specified")
	}

	ts.stopDebugger(t)
}

func TestInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil)

	f := filepath.Join(ts.cwd, "testdata", "go", "helloworld", "main.go")
	ts.setBreakpointAndContinue(t, f, 7)

	text, isErr := ts.callTool(t, "info", map[string]any{"type": "threads"})
	if isErr || !strings.Contains(text, "Thread") {
		t.Fatalf("info threads failed: %s (isErr=%v)", text, isErr)
	}

	text, isErr = ts.callTool(t, "info", map[string]any{})
	if isErr {
		t.Fatalf("info default returned error: %s", text)
	}

	_, isErr = ts.callTool(t, "info", map[string]any{"type": "invalid"})
	if !isErr {
		t.Error("expected error for invalid info type")
	}

	ts.stopDebugger(t)
}

func TestDisassemble(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "step")
	defer cleanupBinary()

	ts.startDebugSession(t, "0", binaryPath, nil)

	_, isErr := ts.callTool(t, "breakpoint", map[string]any{"function": "main.main"})
	if isErr {
		t.Fatal("failed to set breakpoint on main.main")
	}

	ts.callTool(t, "continue", map[string]any{})

	contextStr := ts.getContextContent(t)
	var addr string
	for _, line := range strings.Split(contextStr, "\n") {
		if idx := strings.Index(line, "[ip: "); idx >= 0 {
			if end := strings.Index(line[idx+5:], "]"); end >= 0 {
				addr = line[idx+5 : idx+5+end]
				break
			}
		}
	}
	if addr == "" {
		t.Skip("Could not determine instruction address for disassemble test")
	}

	text, isErr := ts.callTool(t, "disassemble", map[string]any{"address": addr, "count": 5})
	if isErr || !strings.Contains(text, "Disassembly") {
		t.Fatalf("disassemble failed: %s (isErr=%v)", text, isErr)
	}

	ts.stopDebugger(t)
}
