package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// testSetup holds the common test infrastructure
type testSetup struct {
	cwd        string
	binaryPath string
	server     *mcp.Server
	testServer *httptest.Server
	client     *mcp.Client
	session    *mcp.ClientSession
	ctx        context.Context
}

// compileTestProgram compiles the test Go program and returns the binary path
func compileTestProgram(t *testing.T, cwd, name string) (binaryPath string, cleanup func()) {
	t.Helper()

	programPath := filepath.Join(cwd, "testdata", "go", name)
	binaryPath = filepath.Join(programPath, "debugprog")

	os.Remove(binaryPath)

	cmd := exec.Command("go", "build", "-gcflags=all=-N -l", "-o", binaryPath, ".")
	cmd.Dir = programPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to compile program: %v\nOutput: %s", err, output)
	}

	cleanup = func() { os.Remove(binaryPath) }
	return binaryPath, cleanup
}

// setupMCPServerAndClient creates and connects MCP server and client
func setupMCPServerAndClient(t *testing.T) *testSetup {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %v", err)
	}

	implementation := mcp.Implementation{Name: "mcp-dap-server", Version: "v1.0.0"}
	server := mcp.NewServer(&implementation, nil)
	registerTools(server, io.Discard)

	getServer := func(request *http.Request) *mcp.Server { return server }
	sseHandler := mcp.NewSSEHandler(getServer, nil)
	testServer := httptest.NewServer(sseHandler)

	clientImplementation := mcp.Implementation{Name: "test-client", Version: "v1.0.0"}
	client := mcp.NewClient(&clientImplementation, nil)

	ctx := context.Background()
	transport := &mcp.SSEClientTransport{Endpoint: testServer.URL}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("Failed to connect client to server: %v", err)
	}

	return &testSetup{
		cwd:        cwd,
		server:     server,
		testServer: testServer,
		client:     client,
		session:    session,
		ctx:        ctx,
	}
}

func (ts *testSetup) cleanup() {
	if ts.session != nil {
		ts.session.Close()
	}
	if ts.testServer != nil {
		ts.testServer.Close()
	}
}

func (ts *testSetup) startDebugSession(t *testing.T, port string, binaryPath string, breakpoints []map[string]any, programArgs ...string) {
	t.Helper()

	args := map[string]any{
		"mode": "binary",
		"path": binaryPath,
		"port": port,
	}
	if len(breakpoints) > 0 {
		args["breakpoints"] = breakpoints
	}
	if len(programArgs) > 0 {
		args["args"] = programArgs
	}

	text, isErr := ts.callTool(t, "debug", args)
	if isErr {
		t.Fatalf("Debug session returned error: %s", text)
	}
	t.Logf("Debug session started: %s", text)
}

func (ts *testSetup) startDebugSessionWithParams(t *testing.T, args map[string]any) {
	t.Helper()
	text, isErr := ts.callTool(t, "debug", args)
	if isErr {
		t.Fatalf("debug session returned error: %s", text)
	}
	t.Logf("Debug session started via params: %v", args)
}

func (ts *testSetup) setBreakpointAndContinue(t *testing.T, file string, line int) {
	t.Helper()

	result, err := ts.session.CallTool(ts.ctx, &mcp.CallToolParams{
		Name:      "breakpoint",
		Arguments: map[string]any{"file": file, "line": line},
	})
	if err != nil {
		t.Fatalf("Failed to set breakpoint: %v", err)
	}
	t.Logf("Set breakpoint: %v", result)

	result, err = ts.session.CallTool(ts.ctx, &mcp.CallToolParams{
		Name:      "continue",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Failed to continue: %v", err)
	}
	t.Logf("Continue result: %v", result)
}

func (ts *testSetup) getContextContent(t *testing.T) string {
	t.Helper()

	result, err := ts.session.CallTool(ts.ctx, &mcp.CallToolParams{
		Name:      "context",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Failed to get context: %v", err)
	}

	if result.IsError {
		errorMsg := "Unknown error"
		if len(result.Content) > 0 {
			if tc, ok := result.Content[0].(*mcp.TextContent); ok {
				errorMsg = tc.Text
			}
		}
		t.Fatalf("Context returned error: %s", errorMsg)
	}

	if len(result.Content) == 0 {
		t.Fatalf("Expected context content, got empty")
	}

	var contextStr strings.Builder
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			contextStr.WriteString(tc.Text)
		}
	}
	return contextStr.String()
}

func (ts *testSetup) stopDebugger(t *testing.T) {
	t.Helper()

	result, err := ts.session.CallTool(ts.ctx, &mcp.CallToolParams{
		Name:      "stop",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Failed to stop debugger: %v", err)
	}
	t.Logf("Stop debugger result: %v", result)
}

func (ts *testSetup) callTool(t *testing.T, name string, args map[string]any) (string, bool) {
	t.Helper()
	result, err := ts.session.CallTool(ts.ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("Failed to call tool %s: %v", name, err)
	}
	var text strings.Builder
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String(), result.IsError
}

func requireGDBDeps(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gdb"); err != nil {
		t.Skip("gdb not found in PATH")
	}
}

func compileTestCProgram(t *testing.T, cwd, name string) (binaryPath string, cleanup func()) {
	t.Helper()

	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not found in PATH")
	}

	programDir := filepath.Join(cwd, "testdata", "c", name)
	binaryPath = filepath.Join(programDir, "debugprog")

	os.Remove(binaryPath)

	cmd := exec.Command("gcc", "-g", "-O0", "-o", binaryPath, "main.c")
	cmd.Dir = programDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to compile C program: %v\nOutput: %s", err, output)
	}

	cleanup = func() {
		os.Remove(binaryPath)
		os.RemoveAll(binaryPath + ".dSYM")
	}
	return binaryPath, cleanup
}

func generateCoreDump(t *testing.T, binaryPath string) string {
	t.Helper()

	var rLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CORE, &rLimit); err != nil {
		t.Skipf("Cannot get RLIMIT_CORE: %v", err)
	}
	rLimit.Cur = rLimit.Max
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &rLimit); err != nil {
		t.Skipf("Cannot set RLIMIT_CORE: %v", err)
	}

	cmd := exec.Command(binaryPath)
	cmd.Env = append(os.Environ(), "GOTRACEBACK=crash")
	_ = cmd.Run()

	pid := cmd.Process.Pid

	if runtime.GOOS == "linux" {
		if pattern, err := os.ReadFile("/proc/sys/kernel/core_pattern"); err == nil && len(pattern) > 0 && pattern[0] == '|' {
			corePath := filepath.Join(t.TempDir(), fmt.Sprintf("core.%d", pid))
			var dumpErr error
			for range 10 {
				out, err := exec.Command("coredumpctl", "dump", fmt.Sprintf("%d", pid), "--output", corePath).CombinedOutput()
				if err == nil {
					return corePath
				}
				dumpErr = fmt.Errorf("%v: %s", err, out)
				time.Sleep(500 * time.Millisecond)
			}
			t.Skipf("systemd-coredump active but coredumpctl dump failed: %v", dumpErr)
			return ""
		}
	}

	var candidates []string
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, fmt.Sprintf("/cores/core.%d", pid))
	}
	candidates = append(candidates, fmt.Sprintf("core.%d", pid), "core")

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	t.Skip("Could not find core dump file (check ulimit -c and core dump configuration)")
	return ""
}

func TestCompileTestCProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get cwd: %v", err)
	}

	binaryPath, cleanup := compileTestCProgram(t, cwd, "helloworld")
	defer cleanup()

	info, err := os.Stat(binaryPath)
	if err != nil {
		t.Fatalf("Binary not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("Binary is empty")
	}

	cmd := exec.Command(binaryPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Binary failed to run: %v\nOutput: %s", err, output)
	}
	if !strings.Contains(string(output), "Sum: 30") {
		t.Errorf("Expected output to contain 'Sum: 30', got: %s", output)
	}
}

func TestToolListChangesWithCapabilities(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	toolList, err := ts.session.ListTools(ts.ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("Failed to list tools: %v", err)
	}

	toolNames := make(map[string]bool)
	for _, tool := range toolList.Tools {
		toolNames[tool.Name] = true
	}

	if !toolNames["debug"] {
		t.Error("Expected 'debug' tool before session start")
	}
	if toolNames["stop"] {
		t.Error("Did not expect 'stop' tool before session start")
	}

	binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "helloworld")
	defer cleanupBinary()
	ts.startDebugSession(t, "0", binaryPath, nil)

	toolList, err = ts.session.ListTools(ts.ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("Failed to list tools after debug: %v", err)
	}

	toolNames = make(map[string]bool)
	for _, tool := range toolList.Tools {
		toolNames[tool.Name] = true
	}

	if toolNames["debug"] {
		t.Error("Did not expect 'debug' tool during active session")
	}
	for _, name := range []string{"stop", "breakpoint", "continue", "step", "context", "evaluate"} {
		if !toolNames[name] {
			t.Errorf("Expected '%s' tool during active session", name)
		}
	}

	ts.stopDebugger(t)

	toolList, err = ts.session.ListTools(ts.ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("Failed to list tools after stop: %v", err)
	}

	toolNames = make(map[string]bool)
	for _, tool := range toolList.Tools {
		toolNames[tool.Name] = true
	}

	if !toolNames["debug"] {
		t.Error("Expected 'debug' tool after session stop")
	}
	if toolNames["stop"] {
		t.Error("Did not expect 'stop' tool after session stop")
	}
}

func TestErrorBeforeDebuggerStarted(t *testing.T) {
	ts := setupMCPServerAndClient(t)
	defer ts.cleanup()

	toolsToTest := []struct {
		name string
		args map[string]any
	}{
		{"context", map[string]any{}},
		{"continue", map[string]any{}},
		{"breakpoint", map[string]any{"file": "/tmp/test.go", "line": 1}},
		{"step", map[string]any{"mode": "over"}},
		{"stop", map[string]any{}},
		{"evaluate", map[string]any{"expression": "x"}},
		{"info", map[string]any{}},
		{"pause", map[string]any{"threadId": 1}},
		{"clear-breakpoints", map[string]any{"all": true}},
	}

	for _, tt := range toolsToTest {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ts.session.CallTool(ts.ctx, &mcp.CallToolParams{
				Name:      tt.name,
				Arguments: tt.args,
			})
			if err == nil {
				t.Errorf("Expected error calling %s before debugger started, got nil", tt.name)
			}
		})
	}
}
