# AGENTS.md

This file provides guidance to AI coding agents when working with code in this repository.

## Project Overview

This is an MCP (Model Context Protocol) server that bridges MCP clients with DAP (Debug Adapter Protocol) debuggers. It exposes debugging capabilities as MCP tools, allowing AI assistants to programmatically control debuggers. Supports Delve (Go), GDB (C/C++ via native DAP), and vscode-bash-debug (Bash shell scripts).

## Architecture

### Core Components

**main.go**(50 lines): MCP server initialization
- Creates the MCP server using the `go-sdk`
- Registers all debugging tools via `registerTools()`
- Registers workflow prompts via `registerPrompts()`
- Exposes stdio transport

**prompts.go**(599 lines): MCP prompt implementations
- 4 prompt handlers for guided debugging workflows (source, attach, core dump, binary)
- Registered via `server.AddPrompt()` — no session state, always available
- Each prompt returns a `GetPromptResult` with step-by-step tool invocation guidance
- `debug-source` prompt supports `language` parameter ("go", "c"/"cpp", "bash")

**tools.go**(950 lines): MCP tool handler implementations
- All MCP tools are methods on `debuggerSession` struct
- Each tool method signature: `func (ds *debuggerSession) toolName(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[ParamsType]) (*mcp.CallToolResultFor[any], error)`
- Tools send DAP requests via `ds.client` and read/parse responses
- `readAndValidateResponse(client, requestSeq, errorPrefix)` matches responses by `request_seq`, skipping unrelated responses
- `readTypedResponse[T](client, requestSeq)` matches typed responses by `request_seq`, skipping unrelated responses
- Tools that wait for stopped/terminated events loop on `ReadMessage()` until they receive the expected event

**params.go**(106 lines): Tool parameter type definitions
- `DebugParams`, `BreakpointParams`, `ContinueParams`, `StepParams`, `ContextParams`, `EvaluateParams`, `SetVariableParams`, `PauseParams`, `InfoParams`, `DisassembleParams`, `RestartParams`, `StopParams`, `ClearBreakpointsParams`
- `debugToolDescription` const — the full tool description string
- All param structs use JSON/MCP struct tags

**session.go**(353 lines): Shared session state and helpers
- `debuggerSession` struct with all fields (cmd, client, server, backend, capabilities, etc.)
- `registerTools()` — registers all tools on the MCP server
- `registerSessionTools()` / `unregisterSessionTools()` — dynamic tool lifecycle
- `cleanup()`, `getThreadList()`, `getFullContext()`, `stopSummary()`, `writeScopesAndVariables()`
- All tool methods on `debuggerSession` are defined across tools.go

**dap.go**(326 lines): DAP client implementation
- `DAPClient` manages TCP or stdio connection to DAP server
- Wraps `github.com/google/go-dap` protocol messages
- All DAP request methods return `(int, error)` where `int` is the request sequence number
- Sequence numbers are used to match responses to requests via `request_seq` field

**backend.go**(339 lines): Debugger backend abstraction
- `DebuggerBackend` interface: `Spawn()`, `TransportMode()`, `AdapterID()`, `LaunchArgs()`, `CoreArgs()`, `CoreRequestType()`, `AttachArgs()`, `RestartArgs()`, `StdioPipes()`
- `delveBackend`: Spawns `dlv dap`, uses TCP transport
- `gdbBackend`: Spawns `gdb -i dap`, uses stdio transport
- `bashdbBackend`: Spawns `node <adapterPath>`, uses stdio transport (vscode-bash-debug)
- `defaultDebuggerFor(language)`: maps "go"→"delve", "c"/"cpp"→"gdb", "bash"/"sh"→"bash"
- `newBackend(params)`: factory function — single switch constructing the correct backend

**bash_backend.go**(118 lines): Bash debugger backend
- `bashdbBackend` struct with configurable paths: `nodePath`, `adapterPath`, `bashPath`, `catPath`, `mkfifoPath`, `pkillPath`
- Supports `source` and `binary` modes (both launch the script). No core or attach mode.
- Spawns `node <adapterPath>` and connects via stdio

**flexint.go**(36 lines): Flexible integer parsing
- `FlexInt` type handles JSON values that may be integers or string-encoded integers
- Used in tool parameter structs where MCP clients may send numbers as strings

### Key Patterns

1. **Tool Parameter Structs**: Each tool has a corresponding `*Params` struct with JSON/MCP tags, defined in `params.go`
2. **Backend Factory**: `newBackend(params)` in backend.go constructs the correct `DebuggerBackend` based on `debugger` or `language` params. Adding a new backend requires 1 backend file + 1 case in `newBackend()` + 1 mapping in `defaultDebuggerFor()`
3. **DAP Message Handling**: Response reading matches by `request_seq` (from the DAP protocol), skipping unrelated responses from other requests. go-dap decodes all failed responses as `*dap.ErrorResponse`, so matching by Go type alone is insufficient
4. **Event vs Response**: Some operations (continue, step, etc.) wait for `StoppedEvent` or `TerminatedEvent` rather than just response messages
5. **Error Propagation**: DAP response `Success` field is checked; error messages from `response.Message` are wrapped in Go errors
6. **Capability-Gated Tools**: `set-variable`, `disassemble`, and `restart` are only registered when the DAP adapter reports support via `capabilities.Supports*` fields. `ConfigurationDone` is also capability-gated
7. **Dynamic Tool Registration**: Only `debug` is registered initially; session tools replace it after a session starts, then are removed when the session stops
8. **Serialized DAP Access**: A mutex on `debuggerSession` serializes all tool calls, preventing concurrent reads from the single DAP connection

## Development Commands

### Build
```bash
go build -o bin/mcp-dap-server
```

### Run Tests
```bash
# Run all tests
go test -v

# Run a specific test
go test -v -run TestBasic

# Run with race detector
go test -race -v

# Run with coverage
go test -v -coverprofile=coverage.out && go tool cover -func=coverage.out | grep total
```

### Run the Server
```bash
# Default (port 8080)
./bin/mcp-dap-server

# Or via go run
go run .
```

### Test Program Compilation
Go test programs are compiled with debugging symbols during test execution:
```bash
go build -gcflags=all=-N -l -o testdata/go/helloworld/debugprog testdata/go/helloworld/main.go
```
C test programs are compiled with:
```bash
gcc -g -O0 -o testdata/c/helloworld/debugprog testdata/c/helloworld/main.c
```

## MCP Tools (Current API)

Tools are dynamically registered. Before a debug session, only `debug` is available. After starting a session, the following tools are registered:

| Tool | Description |
|------|-------------|
| `debug` | Start a complete debug session (modes: source, binary, core, attach) |
| `stop` | End the debugging session |
| `breakpoint` | Set a breakpoint (file+line or function name) |
| `clear-breakpoints` | Remove breakpoints (by file or all) |
| `continue` | Continue execution (with optional run-to-cursor) |
| `step` | Step over/in/out |
| `pause` | Pause a running program |
| `context` | Get full debugging context (location, stack, variables) |
| `evaluate` | Evaluate an expression |
| `info` | List threads, sources, or modules |
| `set-variable` | Modify a variable value (capability-gated) |
| `disassemble` | Disassemble at address (capability-gated) |
| `restart` | Restart the session (capability-gated) |

### Typical Debugging Flow

1. **debug** — Spawns debugger, connects, optionally sets breakpoints and continues to first hit
2. **context** — Get location, stack trace, and variables at the stop point
3. **breakpoint** / **clear-breakpoints** — Manage breakpoints
4. **continue** / **step** — Navigate through execution
5. **evaluate** — Inspect expressions
6. **stop** — Clean up

## Testing Architecture

Tests in `tools_test.go` follow a common pattern:

1. **Setup**: `setupMCPServerAndClient()` creates an in-process MCP server/client pair
2. **Compilation**: `compileTestProgram()` builds test Go programs with debug flags (`-gcflags=all=-N -l`); `compileTestCProgram()` for C tests
3. **Session Management**: `startDebugSession()` starts a debug session with optional breakpoints
4. **Helpers**: `callTool()` reduces boilerplate for calling tools and extracting text content; `setBreakpointAndContinue()`, `getContextContent()`, `stopDebugger()` handle common operations
5. **Test Programs**:
   - Go (`testdata/go/*/main.go`): `helloworld`, `step`, `scopes`, `restart`, `coredump`, `loop`, `buggy`
   - C (`testdata/c/*/main.c`): `helloworld`, `coredump`
   - Bash (`testdata/bash/`): `hello.sh` — simple script with variables and a function call

### Common Test Flow (Go/Delve)
```go
func TestSomething(t *testing.T) {
    ts := setupMCPServerAndClient(t)
    defer ts.cleanup()

    binaryPath, cleanupBinary := compileTestProgram(t, ts.cwd, "helloworld")
    defer cleanupBinary()

    ts.startDebugSession(t, "0", binaryPath, nil)  // port "0" = auto-assign

    f := filepath.Join(ts.cwd, "testdata", "go", "helloworld", "main.go")
    ts.setBreakpointAndContinue(t, f, 7)

    contextStr := ts.getContextContent(t)
    // Assert on contextStr...

    ts.stopDebugger(t)
}
```

### Common Test Flow (Bash)
```go
func TestBashDebugLaunch(t *testing.T) {
    ts := setupMCPServerAndClient(t)
    defer ts.cleanup()

    adapterPath := os.Getenv("BASH_DEBUG_ADAPTER_PATH")
    if adapterPath == "" {
        t.Skip("vscode-bash-debug adapter not available")
    }

    scriptPath, _ := filepath.Abs(filepath.Join(ts.cwd, "testdata", "bash", "hello.sh"))
    bpFile, _ := filepath.Abs(filepath.Join(ts.cwd, "testdata", "bash", "hello.sh"))

    ts.startDebugSessionWithParams(t, map[string]any{
        "mode":            "source",
        "path":            scriptPath,
        "debugger":        "bash",
        "bashAdapterPath": adapterPath,
        "breakpoints":     []map[string]any{{"file": bpFile, "line": 6}},
    })

    contextStr := ts.getContextContent(t)
    // Assert on contextStr...

    ts.stopDebugger(t)
}
```

## Important Implementation Details

### Multi-Debugger Support
- **Delve** (default): Go programs, TCP transport, spawns `dlv dap`
- **GDB**: C/C++ programs, stdio transport, requires GDB 14+ (native DAP)
- **bashdb** (vscode-bash-debug): Bash shell scripts, stdio transport, requires Node.js and the `vscode-bash-debug` adapter installed
- Backend selection via `debugger` parameter ("delve", "gdb", or "bash") or `language` parameter ("go", "c"/"cpp", "bash"/"sh")
- `newBackend()` factory in backend.go centralizes all backend construction

### State Management
- `debuggerSession` is shared across all tool calls within a session (defined in session.go)
- Only one debugger can be active per MCP server session
- `ds.client == nil` checks protect against calling tools before debugger is started
- Tools are dynamically registered/unregistered as sessions start/stop

### Response Handling
- Some tools read multiple messages (events + responses) in a loop
- `continue` and `step` (all modes) wait for `StoppedEvent` or `TerminatedEvent`
- The `context` tool automatically fetches scopes and variables for the current frame
- All response matching uses `request_seq`, never Go type alone (go-dap decodes failed responses as `*dap.ErrorResponse` regardless of command)

### Tool Naming Convention
- MCP tool names use kebab-case: `clear-breakpoints`, `set-variable`, `step`
- Go function names use camelCase: `clearBreakpoints`, `setVariable`, `step`

## Common Gotchas

1. **Debugger Must Be Started First**: Most tools will error if `ds.client == nil`
2. **Frame IDs vs Thread IDs**: Frame IDs come from stack traces, thread IDs from the threads request. Delve uses frame IDs starting at 1000.
3. **Variables References**: The `variablesReference` in scopes/variables is a DAP protocol identifier, not a simple index. Delve uses frame_id+1 for locals scope (e.g., 1001 for frame 1000).
4. **Stopped Event Format**: Contains `Reason` field ("breakpoint", "function breakpoint", "step", "entry", "pause", etc.) and `ThreadId`
5. **Serialized Tool Calls**: All tool calls are serialized by a mutex. Concurrent MCP tool calls will queue rather than race. Long-running operations (continue, step) hold the lock until completion.
6. **Capability-Gated Tools**: `set-variable`, `disassemble`, `restart`, and `ConfigurationDone` are only sent when the DAP adapter reports support via capabilities
7. **Test Binary Paths**: Must be absolute paths for the `debug` tool in binary mode
8. **go-dap ErrorResponse Decoding**: go-dap decodes ALL failed responses (`success: false`) as `*dap.ErrorResponse` regardless of command. Response matching must use `request_seq`, not Go type
9. **DAP Response Ordering**: Some adapters may send responses out of order (e.g., `StoppedEvent` before `ContinueResponse`). Out-of-order responses are skipped by `request_seq` matching
10. **go-dap `omitempty` on FrameId**: `EvaluateArguments.FrameId` has `omitempty`, so `frameId=0` is silently dropped. `EvaluateRequest` uses raw JSON map to work around this
11. **Bash adapter path**: The `bashAdapterPath` parameter is required when using the bash debugger (points to `out/bashDebug.js`). Bash tests skip if the adapter or Node.js is unavailable.
12. **RestartArgs**: The `RestartArgs()` interface method on `DebuggerBackend` lets backends control restart parameters. Returning `nil, nil` means "let the DAP adapter use its defaults."

## Dependencies

- `github.com/google/go-dap` - DAP protocol implementation
- `github.com/modelcontextprotocol/go-sdk` - MCP server framework
- Requires `dlv` (Delve debugger) in `$PATH` for Go debugging
- Optional: GDB 14+ for C/C++ debugging (native DAP support via `gdb -i dap`)
- Optional: Node.js + vscode-bash-debug adapter for Bash script debugging. Install via `npm install vscode-bash-debug` or set `bashAdapterPath` to the adapter's `out/bashDebug.js`.

## Workflow Guidance

### MCP Prompts

The server exposes 4 prompts (via `prompts/list` and `prompts/get`) that return guided debugging workflows:

| Prompt | Required Args | Use for |
|--------|--------------|---------|
| `debug-source` | `path` | Debugging Go/C/C++/Bash from source |
| `debug-attach` | `pid` | Attaching to a running process |
| `debug-core-dump` | `binary_path`, `core_path` | Post-mortem crash analysis |
| `debug-binary` | `path` | Assembly-level binary debugging |

Prompts are registered in `prompts.go` via `registerPrompts()`, called from `main.go`. The `debug-source` prompt accepts an optional `language` parameter to select the appropriate debugger.

### Human Reference

See `docs/debugging-workflows.md` for:
- Decision table: scenario → mode → which prompt to use
- Mermaid workflow diagrams for each scenario
- Common gotchas and patterns per scenario
