# TODO: Language-to-Debugger Server Configuration

## Goal

Allow users to specify which debugger backend is used for a given language,
with graceful fallback to hardcoded defaults when no configuration is present.

## Precedence Chain (highest to lowest)

```
Tool param `debugger`  →  Env var `MCPDAP_DEBUGGER_<LANG>`  →  hardcoded `defaultDebuggerFor()`
```

Path defaults follow a parallel chain:

```
Tool param `<Backend>Path`  →  Env var `MCPDAP_<BACKEND>_PATH`  →  `exec.LookPath(...)`
```

## Configuration Mechanism

Env vars only. Zero new Go dependencies. Matches MCP ecosystem convention and
maps directly to OpenCode's `"environment"` field in `opencode.json`.

### OpenCode Config Example

```json
{
  "mcp": {
    "dap": {
      "type": "local",
      "command": ["/path/to/bin/mcp-dap-server"],
      "environment": {
        "MCPDAP_DEBUGGER_C": "lldb",
        "MCPDAP_DEBUGGER_CPP": "lldb",
        "MCPDAP_GDB_PATH": "/opt/gdb/bin/gdb",
        "MCPDAP_BASH_ADAPTER_PATH": "/home/user/vscode-bash-debug/out/bashDebug.js"
      }
    }
  }
}
```

### Env Var Naming Convention

```
MCPDAP_DEBUGGER_<LANGUAGE>     →  override debugger for a language
MCPDAP_GDB_PATH               →  path to GDB binary
MCPDAP_BASH_ADAPTER_PATH       →  path to bash-debug-adapter's bashDebug.js
MCPDAP_BASH_NODE_PATH          →  path to Node.js binary
```

Language keys are lowercased env var name suffixes (e.g. `MCPDAP_DEBUGGER_RUST` →
language `rust`).

## Files to Create or Change

### 1. New file: `config.go` (~40 lines)

Load env vars into a `ServerConfig` struct. **Do not scan `os.Environ()`** —
read each known key individually with `os.Getenv`. This avoids silently picking
up typos (`MCPDAP_DEBUGGER_CPPP` would be ingested as language `cppp`).

```go
package main

import "os"

type ServerConfig struct {
    LanguageDebuggers map[string]string
    GDBPath           string
    BashAdapterPath   string
    BashNodePath      string
}

func loadServerConfig() *ServerConfig {
    cfg := &ServerConfig{
        LanguageDebuggers: make(map[string]string),
    }
    for _, lang := range []string{"go", "c", "cpp", "bash", "rust"} {
        if v := os.Getenv("MCPDAP_DEBUGGER_" + strings.ToUpper(lang)); v != "" {
            cfg.LanguageDebuggers[lang] = v
        }
    }
    cfg.GDBPath         = os.Getenv("MCPDAP_GDB_PATH")
    cfg.BashAdapterPath = os.Getenv("MCPDAP_BASH_ADAPTER_PATH")
    cfg.BashNodePath    = os.Getenv("MCPDAP_BASH_NODE_PATH")
    return cfg
}
```

**Open question:** Should the supported language list be hardcoded as above, or
generated from the env vars at runtime? Hardcoded is safer (no silent typos),
but limits extensibility to languages known at compile time. If the debugger
backend set is extensible (new backends added via config), then a runtime scan
is correct — but that should come with validation that the resolved debugger
name maps to a known backend.

### 2. Modifications to `backend.go`

#### `defaultDebuggerFor` gains a `cfg` parameter

```go
func defaultDebuggerFor(language string, cfg *ServerConfig) string {
    if d, ok := cfg.LanguageDebuggers[language]; ok && d != "" {
        return d
    }
    switch language {
    case "go":       return "delve"
    case "c","cpp":  return "gdb"
    case "bash","sh":return "bash"
    }
    return ""
}
```

#### `newBackend` receives resolved values, not raw config

**Critical: Pre-resolve debugger and paths before calling `newBackend`.**
Do not pass `*ServerConfig` into `newBackend`. Extract resolution functions
that produce final values, then pass those to the factory.

```go
func resolveDebugger(params DebugParams, cfg *ServerConfig) string {
    if params.Debugger != "" {
        return params.Debugger
    }
    if params.Language != "" {
        return defaultDebuggerFor(params.Language, cfg)
    }
    return "delve"
}

func resolvePaths(params DebugParams, cfg *ServerConfig) (gdbPath, bashAdapter, bashNode string) {
    gdbPath = params.GDBPath
    if gdbPath == "" { gdbPath = cfg.GDBPath }
    if gdbPath == "" { gdbPath, _ = exec.LookPath("gdb") }

    bashAdapter = params.BashAdapterPath
    if bashAdapter == "" { bashAdapter = cfg.BashAdapterPath }

    bashNode = params.BashNodePath
    if bashNode == "" { bashNode = cfg.BashNodePath }

    return
}
```

#### `newBackend` takes resolved values directly

```go
func newBackend(debugger, gdbPath, toolLogPath, bashAdapter, bashNode string,
    bashPaths BashPaths, programArgs []string) (DebuggerBackend, error) {
    switch debugger {
    case "delve":
        return &delveBackend{}, nil
    case "gdb":
        if gdbPath == "" {
            return nil, fmt.Errorf("GDB not found; install GDB 14+ or set gdbPath / MCPDAP_GDB_PATH")
        }
        return &gdbBackend{gdbPath: gdbPath, toolLogPath: toolLogPath}, nil
    case "bash":
        if bashAdapter == "" {
            return nil, fmt.Errorf("bashAdapterPath is required; set the parameter or MCPDAP_BASH_ADAPTER_PATH")
        }
        return &bashdbBackend{...}, nil
    default:
        return nil, fmt.Errorf("unsupported debugger: %s", debugger)
    }
}
```

This keeps `newBackend` ignorant of config precedence. The merging logic lives
in `resolveDebugger` and `resolvePaths`, both testable in isolation. Future
backend authors don't need to understand the config layer.

### 3. Modifications to `tools.go`

In the `debug()` handler, replace the `newBackend(params)` call with:

```go
resolvedDebugger := resolveDebugger(params, ds.cfg)
resolvedGDB, resolvedBashAdapter, resolvedBashNode := resolvePaths(params, ds.cfg)
backend, err := newBackend(resolvedDebugger, resolvedGDB, params.ToolLog,
    resolvedBashAdapter, resolvedBashNode, bashPathsFromParams(params), params.Args)
```

This requires `debuggerSession` to hold a `cfg *ServerConfig` field, set during
`registerTools`.

### 4. Modifications to `main.go`

```go
cfg := loadServerConfig()
ds := registerTools(server, logWriter, cfg)
```

`registerTools` stores `cfg` on the `debuggerSession`.

## Integration Threading

```
main.go                     session.go              tools.go          backend.go
────────                    ──────────              ────────          ──────────
loadServerConfig() ──→ registerTools(cfg)
                            ds.cfg = cfg
                                                     debug():
                                                       resolveDebugger()
                                                       resolvePaths()
                                                       newBackend()
                                                       defaultDebuggerFor()
```

## Graceful Failure Behavior

| Scenario | Outcome |
|----------|---------|
| No env vars set | Hardcoded `defaultDebuggerFor()` mapping used (current behavior, zero regression) |
| `MCPDAP_DEBUGGER_C=lldb` but no lldb backend | `newBackend` returns `"unsupported debugger: lldb"` — caught at `debug` invocation |
| `MCPDAP_GDB_PATH=/nonexistent` | `debug()` spawn fails — error from `exec.Command` surfaces to the MCP tool result |
| `MCPDAP_DEBUGGER_GO=delve` (redundant) | Works identically, just bypasses the hardcoded switch |
| `MCPDAP_DEBUGGER_C=` (empty value) | Env returns `""`, `defaultDebuggerFor` treats empty as unset, falls through to `gdb` |

## What This Doesn't Do (Intentionally)

| Not doing | Why |
|-----------|-----|
| Config file (YAML/JSON/TOML) | Overkill for <10 values. Env vars are the MCP ecosystem standard. Zero new Go deps. |
| Dynamic reloading | Server is short-lived (one MCP session). Config loaded once at startup. |
| Binary validation at startup | Debuggers may not be on PATH until a debugee is selected. Validation happens at invocation. |
| Per-tool config | One server = one config. Different mappings require separate server instances. |

## Design Properties

- **Loose coupling**: `newBackend` receives resolved values, not raw config. Config merging is isolated in resolution functions. Backend constructors don't need to know about env vars.
- **Extensible**: New language→debugger mappings work via env vars without code changes. New backends require a case in `newBackend`'s switch, which is unavoidable — the backend struct needs to exist.
- **Zero regression**: No env vars → identical behavior to current code. The hardcoded switch in `defaultDebuggerFor` is the bottom of the precedence chain, never removed.
- **Testable**: `resolveDebugger` and `resolvePaths` are pure functions that take structs and return values. No env var access, no I/O.

## Verification

- [ ] `go build ./...` succeeds with no new dependencies in `go.mod`
- [ ] Default path (no env vars): identical behavior to current
- [ ] `MCPDAP_DEBUGGER_C=lldb` returns error "unsupported debugger: lldb" (no lldb backend)
- [ ] `MCPDAP_DEBUGGER_GO=gdb` → go source mode fails (gdb rejects source mode)
- [ ] `MCPDAP_GDB_PATH` overrides default gdb lookup
- [ ] Tool-param `"debugger": "delve"` overrides `MCPDAP_DEBUGGER_GO=gdb`
- [ ] Empty env var values fall through to defaults
- [ ] All existing tests pass unchanged (no env vars set → current behavior)

## Related

- `ARCHITECTURE_REVIEW.md` — full architecture assessment
- `PROTOCOL_COVERAGE.md` — DAP and MCP protocol coverage analysis

---

## Interface Contract Improvements

Architectural evaluation of the three core contracts. See `docs/architecture.md` for context.

## `DebuggerBackend` (`backend.go:15`)

### Transport modality leaks through `Spawn()`

| Issue | Detail |
|-------|--------|
| `listenAddr string` return | TCP-only data. Stdio backends return `""`. Caller must know transport type to interpret. |
| `port string` parameter | Dead weight for 2 of 3 implementations (gdb, bashdb ignore it). |
| `TransportMode()` string discriminator | Callers branch on `"tcp"` vs `"stdio"` — the interface failed to abstract transport. |

**Target**: Replace `Spawn(port, stderrWriter) (cmd, listenAddr, err)` with `Spawn(stderrWriter) (io.ReadWriteCloser, error)`. TCP backends return `net.Conn`; stdio backends return pipe adapter. Port/listen logic becomes an internal detail of `delveBackend`. Eliminate `TransportMode()` and `StdioPipes()`.

### Temporal coupling in `StdioPipes()`

`Spawn()` mutates the backend struct (`g.stdin = stdin`), then `StdioPipes()` returns those values. Nothing enforces call ordering — calling `StdioPipes()` before `Spawn()` silently returns nil pipes. Fixed if merged into the `Spawn()` return above.

### `CoreRequestType()` exposes DAP protocol inconsistency

Delve uses `"launch"` for core dumps, GDB uses `"attach"`. This protocol quirk bubbles through the abstraction and forces `tools.go` to pick DAP request types — a layer violation. **Target**: Move the launch-vs-attach decision into each backend's `Spawn`/connect flow, or collapse into a single `debugCoreDump(programPath, coreFilePath)` method that backends implement opaquely.

### `map[string]any` erases type safety on 4 methods

`LaunchArgs`, `CoreArgs`, `AttachArgs`, `RestartArgs` all return `map[string]any`. No compile-time key validation. A typo silently produces wrong DAP messages. **Target**: Keep `map[string]any` (DAP demands runtime-flexible args) but add a `Validate() error` pass before serialization, or define a `DAPArguments` type with typed builder methods.

### Interface too large for supported modes

10 methods. `bashdbBackend` returns errors for `CoreArgs`, `AttachArgs`. Splitting into capability interfaces (`Launcher`, `Attacher`, `CoreDumper`) would let backends implement only what they support, with the factory reporting capabilities to callers.

### No resource lifecycle contract

`Spawn()` returns `*exec.Cmd`. Who calls `cmd.Wait()`? `debuggerSession.cleanup()` does, but nothing enforces this. If `ds.cmd` is reassigned before the old cmd is waited, we leak a zombie process. **Target**: Backends own the lifecycle — add a `Close()` method to the interface.

## `DAPClient` (`dap.go:25`)

### No interface — concrete type welded to all consumers

Every function in `tools.go` and `session.go` takes `*DAPClient` directly. Consequences: no mocking for isolation tests, no alternative transport implementations (WebSocket, in-process), every consumer must change to add a new client.

### Send and receive split across layers

Client methods return `(seq int, err error)`. Response matching logic (`readAndValidateResponse`, `readTypedResponse`) lives in `tools.go`. Nothing enforces that callers use the seq correctly. **Target**: Pair send+receive in the client (e.g. `ContinueRequest → (StoppedEvent, error)`) or define a `ResponseWaiter` type returned by send methods.

### `InitializeRequest` is the anomaly

Only request method that reads its own response (calls `ReadMessage()` internally). Every other method just sends. The pattern is identical (send → match response by seq), so the inconsistency is architecturally unmotivated.

### No thread-safety guarantees

`rwc`, `seq`, `logWriter` are mutable and unsynchronized. Only the external `debuggerSession.mu` makes access safe. The invariant lives outside the client. **Target**: Either document that the client is not thread-safe (caller must serialize) or add internal synchronization.

### `Close()` not idempotent

Calling `Close()` on a closed TCP connection panics. `debuggerSession.cleanup()` guards with `ds.client == nil`, but the guard is at the wrong layer.

## `debuggerSession` (`session.go:16`)

### 12 mutable fields with undocumented dependencies

- `stoppedThreadID` must be set before `defaultThreadID()` returns meaningfully
- `lastFrameID` uses sentinel `-1` for "unset"
- `client == nil` conflates three states: never started, cleanly stopped, crashed

### Dynamic tool registration invisible to MCP clients

Tool listings differ depending on session state. No way to query "is a session active?" except by trying a call and watching it fail. **Target**: Export a `session-state` tool (always registered) that reports status, or use MCP server prompts to expose session metadata.

### `registerSessionTools()` not idempotent

Calling twice without `unregisterSessionTools()` between causes silent state drift because `RemoveTools("debug")` on the second call drops the first registration.

## Implementation Order

| Priority | Change | Reason |
|----------|--------|--------|
| 1 | Extract `DAPClient` interface | Unblocks mock-based unit testing for all tool handlers |
| 2 | Merge transport into `Spawn()` return | Eliminates `TransportMode()` + `StdioPipes()` + `port` parameter — 3 methods collapse into 1 |
| 3 | Pair send+receive in DAPClient | Moves seq-matching responsibility from tools.go into the client |
| 4 | Split `DebuggerBackend` by capability | Let backends implement only modes they support |
| 5 | Add `Close()` to `DebuggerBackend` | Formalize resource lifecycle |
| 6 | Export session state tool | Make session status queryable by MCP clients |

---

## Exception Breakpoints (setExceptionBreakpoints + exception-info)

### Functional Requirements

1. AI can request that the debugger stop on specific exception types (Go panics, C++
   exceptions, etc.) by passing `exceptionFilters` in the `debug` tool call.
2. AI can inspect exception details (exception ID, description, break mode, exception
   type name, inner exceptions) when stopped at an exception via a new
   `exception-info` tool.
3. The `info` tool can list available exception filter names from the adapter.
4. Tools are capability-gated: only register when the adapter reports
   `ExceptionBreakpointFilters` in `InitializeResponse.Body`.

### Implementation Details

**New DAP request methods** (`dap.go`):

- `SetExceptionBreakpointsRequest(filters []string) (int, error)` — raw JSON
  construction (go-dap v0.12.0 lacks `SetExceptionBreakpointsRequest` type).
- `ExceptionInfoRequest(threadID int) (int, error)` — raw JSON construction (go-dap
  v0.12.0 lacks `ExceptionInfoRequest` type).

**Parameter changes** (`params.go`):

- `DebugParams.ExceptionFilters []string` — exception breakpoint filter names to
  enable. E.g. `"panic"` (Go/Delve), `"cpp_throw"` (C++/GDB).
- New `ExceptionInfoParams` struct with `ThreadID FlexInt`.

**Session lifecycle** (`session.go`):

- `registerSessionTools()`: gated on `len(ds.capabilities.ExceptionBreakpointFilters) > 0`.
- `sessionToolNames()`: add `"exception-info"` when filters exist.
- `info` tool description dynamic extension for `"exception-filters"` type.

**Tool handler** (`tools.go`):

- New `exceptionInfo()` handler — sends `ExceptionInfoRequest`, formats
  `ExceptionInfoResponse` (exception ID, description, break mode, details).
- `configureSession()`: after breakpoints, send `setExceptionBreakpoints` with
  `params.ExceptionFilters` before `configurationDone`.
- `info()`: add `case "exception-filters"` to list available filters from
  `ds.capabilities.ExceptionBreakpointFilters`.

**Capability gating**: go-dap v0.12.0 includes `ExceptionBreakpointFilters` in
the `Capabilities` struct. All exception-related DAP types
(`SetExceptionBreakpointsRequest`, `ExceptionInfoRequest`,
`ExceptionBreakpointsFilter`, `ExceptionBreakMode`, `ExceptionDetails`,
`ExceptionFilterOptions`, `ExceptionOptions`, `ExceptionPathSegment`) are present
and usable directly — no raw JSON workarounds needed.

### Upstream SDK Investigation — Complete

Investigation date: 2026-06-09. **Result: No upstream PR needed.** Verified via
`go doc` that go-dap v0.12.0 contains every exception-related DAP type listed
above. An initial search of the module cache returned empty results due to a
stale cache, but `go doc` confirmed all types are present. Implementation can
proceed with typed go-dap structs — no raw JSON workarounds needed.

Full inventory in `handoff_go_dap_exception_types.md`.

### Verification

- [ ] `go build ./...` succeeds
- [ ] `SetExceptionBreakpointsRequest` compiles with typed go-dap structs
- [ ] `exception-info` tool listed in MCP tools/list when adapter has exception filters
- [ ] `exception-info` tool NOT listed when adapter has no exception filters
- [ ] `debug(mode="source", exceptionFilters=["panic"])` sets exception breakpoints
- [ ] `exception-info` returns structured exception data after a panic stop
- [ ] `info(type="exception-filters")` lists available filters
- [ ] Response matching via `readTypedResponse` works for both new request types

---

## Read Timeouts and Cancellation

### Problem

All DAP response-reading loops (`continueExecution`, `step`, `handleFirstStop`,
`waitForInitialized`, `readAndValidateResponse`, `readTypedResponse`) call
`client.ReadMessage()` in unbounded `for {}` loops. If the DAP adapter crashes,
hangs, or stops responding mid-session, the tool call blocks indefinitely,
holding the `debuggerSession` mutex and freezing all further operations.

The go-sdk v1.6.1 passes `context.Context` to every tool handler, and the MCP
transport supports `notifications/cancelled`. Both are available but not wired
to DAP reads.

### Target

1. **Add context-aware read to `DAPClient`**: a `ReadMessageWithContext(ctx)`
   method that uses a goroutine + `select` on `ctx.Done()` to return an error
   when the context is cancelled. Existing `ReadMessage()` remains for backward
   compatibility; callers inside event-waiting loops switch to the context-aware
   variant.

2. **Wire the `ctx` parameter from tool handlers**: All tool handlers already
   receive `ctx context.Context` as their first parameter — the context is
   simply ignored during DAP reads. Pass it through.

3. **Send `cancel` DAP request on timeout**: If the context is cancelled (the
   MCP client sent `notifications/cancelled`), send a DAP `cancel` request with
   the in-flight request's sequence number before returning. Gate on
   `capabilities.SupportsCancelRequest`.

4. **Add a `readTimeout` constant**: Default 30 seconds for tool calls. The
   context is wrapped with `context.WithTimeout` to prevent indefinite hangs
   even if the MCP client doesn't cancel.

### Files

- `dap.go` — new `ReadMessageWithContext(ctx) (dap.Message, error)` method
- `dap.go` — new `CancelRequest(requestId int) (int, error)` method (raw JSON,
  go-dap v0.12.0 lacks the type)
- `tools.go` — replace `client.ReadMessage()` with `client.ReadMessageWithContext(ctx)`
  in all event-waiting loops
- `session.go` — same for `readAndValidateResponse`, `readTypedResponse`
  (thread context through or accept a `ctx` parameter)

### Verification

- [ ] `go build ./...` succeeds
- [ ] Tool call with active context returns `context.Canceled` error when cancelled
- [ ] `cancel` DAP request sent when `SupportsCancelRequest` is true
- [ ] No regressions in normal (non-cancelled) execution
- [ ] Timeout fires and releases mutex when adapter hangs

---

## MCP Integration Improvements

### MCP Logging

The go-sdk auto-advertises the `logging` capability but `ss.Log()` is never
called. Use it for session lifecycle events:

```
ss.Log(ctx, &mcp.LogParams{Level: "notice", Data: "Debug session started: mode=source, path=main.go"})
ss.Log(ctx, &mcp.LogParams{Level: "notice", Data: "Breakpoint hit: main.go:42"})
ss.Log(ctx, &mcp.LogParams{Level: "warning", Data: "Debug session terminated unexpectedly"})
```

If the logging capability should not be advertised, set
`ServerOptions.Capabilities.Logging` to nil in `main.go`.

### MCP Progress Reporting

Long operations (debug adapter spawn, wait-for-breakpoint) can take several
seconds. Use `ss.NotifyProgress()` with progress tokens when the MCP client
provides them in the `_meta.progressToken` field.

Requires the client to include `_meta.progressToken` in the tool call — the
go-sdk handles token routing automatically for `Tools: { listChanged: true }`.

### setExpression Tool

More natural for AI interaction than `set-variable` — the AI can say "set x to
42" directly via `evaluate("x = 42", context="repl")` rather than needing a
`variablesReference` from a prior `context` call.

**Gating**: `capabilities.SupportsSetExpression`.
**Params**: `SetExpressionParams{Expression string, Value string, FrameId FlexInt}`.
**Tool name**: `set-expression`.

### Step Granularity

The `step` tool accepts `mode: "over"/"in"/"out"`. Extend with an optional
`granularity` parameter:

```go
type StepParams struct {
    Mode        string  // existing
    Granularity string  // NEW: "statement" (default) or "instruction"
    ...
}
```

Passes `granularity` to the `next`/`stepIn`/`stepOut` DAP requests.
**Gating**: `capabilities.SupportsSteppingGranularity`.

### VariablePresentationHint (partially resolved)

Frame-level `PresentationHint == "subtle"` is now surfaced as `" (runtime)"` in
stack trace output (`session.go:274`).

Remaining: the `Variable` type from go-dap has `PresentationHint` with fields
`Kind` ("class", "interface", "method", "property", "data", "virtual"),
`Attributes` ("static", "constant", "readOnly"), and `Visibility` ("public",
"private"). This metadata is available in DAP responses but not surfaced in
`writeScopesAndVariables()`.

Include variable hints in the formatted output:

```
  count (int) = 42 [static]
  handler (Handler) = {Method: processRequest} [public method]
```

**No new tool** — purely a formatting change in `session.go:writeScopesAndVariables`.

### Files

- `session.go` — `writeScopesAndVariables()` hint formatting
- `tools.go` — optional progress tokens in long operations
- `params.go` — `SetExpressionParams`, `StepParams.Granularity`
- `main.go` — logging capability configuration
- `dap.go` — `SetExpressionRequest` method (raw JSON, go-dap may lack the type)

### Verification

- [ ] `go build ./...` succeeds
- [ ] MCP logging events appear when server is connected
- [ ] `set-expression` tool appears in tools/list when adapter supports it
- [ ] `step(granularity="instruction")` sends correct DAP request
- [ ] Variable hints appear in `context()` output
