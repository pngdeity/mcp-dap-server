package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/go-dap"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pngdeity/mcp-dap-server/debugadapters"
)

// readAndValidateResponse reads DAP messages until it receives the response
// matching requestSeq. Out-of-order responses (different request_seq) and
// events are skipped. Returns an error if the matched response indicates failure.
func readAndValidateResponse(client *DAPClient, requestSeq int, errorPrefix string) error {
	for {
		msg, err := client.ReadMessage()
		if err != nil {
			return err
		}
		switch resp := msg.(type) {
		case dap.ResponseMessage:
			r := resp.GetResponse()
			if r.RequestSeq != requestSeq {
				log.Printf("readAndValidateResponse: skipping out-of-order response (request_seq=%d, waiting for %d)",
					r.RequestSeq, requestSeq)
				continue
			}
			if !r.Success {
				return fmt.Errorf("%s: %s", errorPrefix, r.Message)
			}
			return nil
		case dap.EventMessage:
			continue
		default:
			log.Printf("readAndValidateResponse: skipping unexpected message type %T (waiting for request_seq=%d)", msg, requestSeq)
			continue
		}
	}
}

// readTypedResponse reads DAP messages until it receives a response of type T
// matching requestSeq. Out-of-order responses (different request_seq) and
// events are skipped. Returns an error if the matched response indicates failure.
//
// go-dap decodes all failed responses as *dap.ErrorResponse regardless of
// command, so we match by request_seq rather than Go type alone.
func readTypedResponse[T dap.ResponseMessage](client *DAPClient, requestSeq int) (T, error) {
	var zero T
	for {
		msg, err := client.ReadMessage()
		if err != nil {
			return zero, err
		}
		switch resp := msg.(type) {
		case T:
			r := resp.GetResponse()
			if r.RequestSeq != requestSeq {
				log.Printf("readTypedResponse: skipping out-of-order %T (request_seq=%d, waiting for %d)",
					resp, r.RequestSeq, requestSeq)
				continue
			}
			if !r.Success {
				return zero, errors.New(r.Message)
			}
			return resp, nil
		case dap.ResponseMessage:
			r := resp.GetResponse()
			if r.RequestSeq != requestSeq {
				log.Printf("readTypedResponse: skipping out-of-order %T (request_seq=%d, waiting for %d)",
					resp, r.RequestSeq, requestSeq)
				continue
			}
			// Matched request_seq but different Go type (e.g. *dap.ErrorResponse).
			if !r.Success {
				return zero, errors.New(r.Message)
			}
			return zero, fmt.Errorf("expected %T, got %T (request_seq=%d)", zero, resp, requestSeq)
		case dap.EventMessage:
			continue
		default:
			log.Printf("readTypedResponse: skipping unexpected message type %T (waiting for request_seq=%d)", msg, requestSeq)
			continue
		}
	}
}

// maxOutputLines caps the number of program output lines buffered during
// continue/step to prevent token blowout from unbounded output.
const maxOutputLines = 200

// appendOutputEvent buffers a DAP OutputEvent (stdout/stderr only) into buf.
// Returns the updated line count. Stops appending once maxOutputLines is reached.
func appendOutputEvent(buf *strings.Builder, event *dap.OutputEvent, lines int) int {
	if event.Body.Category != "stdout" && event.Body.Category != "stderr" {
		return lines
	}
	if lines >= maxOutputLines {
		return lines
	}
	for _, ch := range event.Body.Output {
		buf.WriteRune(ch)
		if ch == '\n' {
			lines++
			if lines >= maxOutputLines {
				buf.WriteString("... (output truncated after 200 lines)\n")
				return lines
			}
		}
	}
	return lines
}

// prependOutputToResult prepends program output text to a CallToolResult's
// first TextContent block. The result is modified in place.
func prependOutputToResult(result *mcp.CallToolResult, output string) *mcp.CallToolResult {
	if output == "" || len(result.Content) == 0 {
		return result
	}
	if tc, ok := result.Content[0].(*mcp.TextContent); ok {
		tc.Text = "Program Output:\n" + output + "\n" + tc.Text
	}
	return result
}

// clearBreakpoints removes breakpoints.
func (ds *debuggerSession) clearBreakpoints(ctx context.Context, _ *mcp.CallToolRequest, params ClearBreakpointsParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.client == nil {
		return nil, nil, fmt.Errorf("debugger not started")
	}

	if params.All {
		// Clear all function breakpoints
		seq, err := ds.client.SetFunctionBreakpointsRequest([]string{})
		if err != nil {
			return nil, nil, err
		}
		if err := readAndValidateResponse(ds.client, seq, "unable to clear breakpoints"); err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Cleared all breakpoints"}},
		}, nil, nil
	}

	if params.File != "" {
		// Clear breakpoints in specific file by setting empty list
		seq, err := ds.client.SetBreakpointsRequest(params.File, []int{})
		if err != nil {
			return nil, nil, err
		}
		if err := readAndValidateResponse(ds.client, seq, "unable to clear breakpoints"); err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Cleared breakpoints in: %s", params.File)}},
		}, nil, nil
	}

	return nil, nil, fmt.Errorf("specify 'file' or 'all'")
}

// continueExecution continues execution and returns full context when stopped.
func (ds *debuggerSession) continueExecution(ctx context.Context, _ *mcp.CallToolRequest, params ContinueParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.client == nil {
		return nil, nil, fmt.Errorf("debugger not started")
	}

	// If "to" is specified, set a temporary breakpoint
	if params.To != nil {
		to := params.To
		var bpSeq int
		var bpErr error
		if to.Function != "" {
			bpSeq, bpErr = ds.client.SetFunctionBreakpointsRequest([]string{to.Function})
		} else if to.File != "" && to.Line > 0 {
			bpSeq, bpErr = ds.client.SetBreakpointsRequest(to.File, []int{to.Line})
		}
		if bpErr != nil {
			return nil, nil, bpErr
		}
		if err := readAndValidateResponse(ds.client, bpSeq, "unable to set temporary breakpoint"); err != nil {
			return nil, nil, err
		}
	}

	threadID := params.ThreadID.Int()
	if threadID == 0 {
		threadID = ds.defaultThreadID()
	}
	continueSeq, err := ds.client.ContinueRequest(threadID)
	if err != nil {
		return nil, nil, err
	}

	var outputBuf strings.Builder
	var outputLines int
	for {
		msg, err := ds.client.ReadMessage()
		if err != nil {
			return nil, nil, err
		}
		switch resp := msg.(type) {
		case dap.ResponseMessage:
			r := resp.GetResponse()
			if r.RequestSeq != continueSeq {
				log.Printf("continueExecution: skipping out-of-order response (request_seq=%d, waiting for %d)", r.RequestSeq, continueSeq)
				continue
			}
			if !r.Success {
				return nil, nil, fmt.Errorf("continue failed: %s", r.Message)
			}
		case *dap.OutputEvent:
			if params.IncludeOutput {
				outputLines = appendOutputEvent(&outputBuf, resp, outputLines)
			}
			continue
		case *dap.StoppedEvent:
			ds.stoppedThreadID = resp.Body.ThreadId
			ds.lastHitBreakpointIds = resp.Body.HitBreakpointIds
			result, err := ds.getFullContext(resp.Body.ThreadId, 0, 20)
			if outputBuf.Len() > 0 {
				result = prependOutputToResult(result, outputBuf.String())
			}
			if err != nil || params.FullContext {
				return result, nil, err
			}
			return stopSummary(result, resp.Body.Reason, resp.Body.HitBreakpointIds), nil, nil
		case *dap.TerminatedEvent:
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "Program terminated"}},
			}, nil, nil
		}
	}
}

// pauseExecution pauses execution of a thread.
func (ds *debuggerSession) pauseExecution(ctx context.Context, _ *mcp.CallToolRequest, params PauseParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.client == nil {
		return nil, nil, fmt.Errorf("debugger not started")
	}
	seq, err := ds.client.PauseRequest(params.ThreadID.Int())
	if err != nil {
		return nil, nil, err
	}
	if err := readAndValidateResponse(ds.client, seq, "unable to pause execution"); err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "Paused execution"}},
	}, nil, nil
}

// evaluateExpression evaluates an expression in the context of a stack frame.
func (ds *debuggerSession) evaluateExpression(ctx context.Context, _ *mcp.CallToolRequest, params EvaluateParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.client == nil {
		return nil, nil, fmt.Errorf("debugger not started")
	}

	evalContext := params.Context
	if evalContext == "" {
		evalContext = "watch"
	}

	var frameID int
	if params.FrameID != nil {
		frameID = params.FrameID.Int()
	} else if ds.lastFrameID >= 0 {
		frameID = ds.lastFrameID
	}
	log.Printf("evaluate: expression=%q frameID=%d context=%q", params.Expression, frameID, evalContext)

	evalSeq, err := ds.client.EvaluateRequest(params.Expression, frameID, evalContext)
	if err != nil {
		return nil, nil, err
	}

	for {
		msg, err := ds.client.ReadMessage()
		if err != nil {
			return nil, nil, err
		}
		switch resp := msg.(type) {
		case *dap.EvaluateResponse:
			if resp.GetResponse().RequestSeq != evalSeq {
				log.Printf("evaluate: skipping out-of-order EvaluateResponse (request_seq=%d, waiting for %d)",
					resp.GetResponse().RequestSeq, evalSeq)
				continue
			}
			if !resp.Success {
				return nil, nil, fmt.Errorf("unable to evaluate expression: %s", resp.Message)
			}
			result := resp.Body.Result
			if resp.Body.Type != "" {
				result = fmt.Sprintf("%s (type: %s)", resp.Body.Result, resp.Body.Type)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: result}},
			}, nil, nil
		case dap.ResponseMessage:
			r := resp.GetResponse()
			if r.RequestSeq == evalSeq {
				if !r.Success {
					return nil, nil, fmt.Errorf("unable to evaluate expression: %s", r.Message)
				}
			}
			log.Printf("evaluate: skipping out-of-order %T response (request_seq=%d, waiting for %d)",
				resp, r.RequestSeq, evalSeq)
			continue
		case dap.EventMessage:
			continue
		}
	}
}

// setVariable sets the value of a variable in the debugged program.
func (ds *debuggerSession) setVariable(ctx context.Context, _ *mcp.CallToolRequest, params SetVariableParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.client == nil {
		return nil, nil, fmt.Errorf("debugger not started")
	}
	seq, err := ds.client.SetVariableRequest(params.VariablesReference.Int(), params.Name, params.Value)
	if err != nil {
		return nil, nil, err
	}
	if err := readAndValidateResponse(ds.client, seq, "unable to set variable"); err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Set variable %s to %s", params.Name, params.Value)}},
	}, nil, nil
}

// restartDebugger restarts the debugging session.
func (ds *debuggerSession) restartDebugger(ctx context.Context, _ *mcp.CallToolRequest, params RestartParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.client == nil {
		return nil, nil, fmt.Errorf("debugger not started")
	}
	restartArgs, err := ds.backend.RestartArgs(params.Args)
	if err != nil {
		return nil, nil, err
	}
	seq, err := ds.client.RestartRequest(restartArgs)
	if err != nil {
		return nil, nil, err
	}
	if err := readAndValidateResponse(ds.client, seq, "unable to restart debugger"); err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "Restarted debugging session"}},
	}, nil, nil
}

// info returns program metadata.
func (ds *debuggerSession) info(ctx context.Context, _ *mcp.CallToolRequest, params InfoParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.client == nil {
		return nil, nil, fmt.Errorf("debugger not started")
	}

	infoType := params.Type
	if infoType == "" {
		if ds.capabilities.SupportsLoadedSourcesRequest {
			infoType = "sources"
		} else {
			infoType = "threads"
		}
	}

	switch infoType {
	case "threads":
		seq, err := ds.client.ThreadsRequest()
		if err != nil {
			return nil, nil, err
		}
		resp, err := readTypedResponse[*dap.ThreadsResponse](ds.client, seq)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get threads: %w", err)
		}
		var threads strings.Builder
		threads.WriteString("Threads:\n")
		for _, t := range resp.Body.Threads {
			fmt.Fprintf(&threads, "  Thread %d: %s\n", t.Id, t.Name)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: threads.String()}},
		}, nil, nil

	case "sources":
		if !ds.capabilities.SupportsLoadedSourcesRequest {
			return nil, nil, fmt.Errorf("loaded sources not supported by this debug adapter")
		}
		seq, err := ds.client.LoadedSourcesRequest()
		if err != nil {
			return nil, nil, err
		}
		resp, err := readTypedResponse[*dap.LoadedSourcesResponse](ds.client, seq)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get loaded sources: %w", err)
		}
		var sources strings.Builder
		sources.WriteString("Loaded Sources:\n")
		for _, src := range resp.Body.Sources {
			fmt.Fprintf(&sources, "  %s\n", src.Path)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: sources.String()}},
		}, nil, nil

	case "modules":
		if !ds.capabilities.SupportsModulesRequest {
			return nil, nil, fmt.Errorf("modules not supported by this debug adapter")
		}
		seq, err := ds.client.ModulesRequest()
		if err != nil {
			return nil, nil, err
		}
		resp, err := readTypedResponse[*dap.ModulesResponse](ds.client, seq)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get modules: %w", err)
		}
		var modules strings.Builder
		modules.WriteString("Loaded Modules:\n")
		for _, mod := range resp.Body.Modules {
			fmt.Fprintf(&modules, "  %s (%s)\n", mod.Name, mod.Path)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: modules.String()}},
		}, nil, nil

	case "registers":
		if ds.lastFrameID < 0 {
			return nil, nil, fmt.Errorf("no frame available; call 'context' first to stop at a location")
		}
		scopesSeq, err := ds.client.ScopesRequest(ds.lastFrameID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get scopes: %w", err)
		}
		scopesResp, err := readTypedResponse[*dap.ScopesResponse](ds.client, scopesSeq)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get scopes: %w", err)
		}
		for _, scope := range scopesResp.Body.Scopes {
			if scope.Name != "Registers" {
				continue
			}
			if scope.VariablesReference <= 0 {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "No registers available"}},
				}, nil, nil
			}
			varSeq, err := ds.client.VariablesRequest(scope.VariablesReference)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get registers: %w", err)
			}
			varResp, err := readTypedResponse[*dap.VariablesResponse](ds.client, varSeq)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get registers: %w", err)
			}
			var regs strings.Builder
			regs.WriteString("Registers:\n")
			for _, v := range varResp.Body.Variables {
				fmt.Fprintf(&regs, "  %s = %s\n", v.Name, v.Value)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: regs.String()}},
			}, nil, nil
		}
		return nil, nil, fmt.Errorf("registers not available (adapter did not report a Registers scope)")

	default:
		return nil, nil, fmt.Errorf("invalid type: %s (must be 'threads', 'sources', 'modules', or 'registers')", infoType)
	}
}

// disassembleCode disassembles code at a memory reference.
func (ds *debuggerSession) disassembleCode(ctx context.Context, _ *mcp.CallToolRequest, params DisassembleParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	log.Printf("disassemble: address=%s offset=%d", params.Address, params.Offset.Int())
	if ds.client == nil {
		return nil, nil, fmt.Errorf("debugger not started")
	}
	count := params.Count.Int()
	if count == 0 {
		count = 20
	}
	seq, err := ds.client.DisassembleRequest(params.Address, params.Offset.Int(), count)
	if err != nil {
		return nil, nil, err
	}

	disResp, err := readTypedResponse[*dap.DisassembleResponse](ds.client, seq)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to disassemble: %w", err)
	}

	var result strings.Builder
	result.WriteString("Disassembly:\n")
	for _, inst := range disResp.Body.Instructions {
		fmt.Fprintf(&result, "  %s  %s", inst.Address, inst.Instruction)
		if inst.Symbol != "" {
			fmt.Fprintf(&result, "  <%s>", inst.Symbol)
		}
		if inst.Location != nil && inst.Location.Path != "" {
			fmt.Fprintf(&result, "  ; %s:%d", inst.Location.Path, inst.Line)
			if inst.EndLine > 0 && inst.EndLine != inst.Line {
				fmt.Fprintf(&result, "-%d", inst.EndLine)
			}
		}
		result.WriteString("\n")
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: result.String()}},
	}, nil, nil
}

// stop ends the debugging session.
// If params.Detach is true, a DAP disconnect request is sent with terminateDebuggee=false
// so the debuggee keeps running after the adapter disconnects.
func (ds *debuggerSession) stop(ctx context.Context, _ *mcp.CallToolRequest, params StopParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	log.Printf("stop")
	if ds.cmd == nil && ds.client == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "No debug session active"}},
		}, nil, nil
	}

	if params.Detach && ds.client != nil {
		// Send disconnect with terminateDebuggee=false so the debuggee keeps running.
		seq, err := ds.client.DisconnectRequest(false)
		if err != nil {
			log.Printf("stop: disconnect request failed: %v", err)
		} else {
			if err := readAndValidateResponse(ds.client, seq, "disconnect"); err != nil {
				log.Printf("stop: disconnect response error: %v", err)
			}
		}
		ds.cleanup()
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Detached from process (debuggee still running)"}},
		}, nil, nil
	}

	ds.cleanup()

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "Debug session stopped"}},
	}, nil, nil
}

// debug starts a complete debugging session.
// It starts the debugger, loads the program, sets initial breakpoints, and runs to the first breakpoint.
func (ds *debuggerSession) debug(ctx context.Context, _ *mcp.CallToolRequest, params DebugParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.cleanup()

	port, mode, err := ds.validateDebugParams(params)
	if err != nil {
		return nil, nil, err
	}
	if err := ds.spawnAndConnect(port, params.ProtocolLog); err != nil {
		return nil, nil, err
	}
	if err := ds.startSession(params, mode); err != nil {
		return nil, nil, err
	}
	if err := ds.waitForInitialized(); err != nil {
		return nil, nil, err
	}
	if err := ds.configureSession(params.Breakpoints); err != nil {
		return nil, nil, err
	}
	ds.registerSessionTools()
	return ds.handleFirstStop(params, mode)
}

func (ds *debuggerSession) validateDebugParams(params DebugParams) (port, mode string, err error) {
	port = params.Port
	if port == "" {
		port = "0"
	}
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}

	mode = params.Mode
	switch mode {
	case "source", "binary", "core", "attach":
	default:
		return "", "", fmt.Errorf("invalid mode: %s (must be 'source', 'binary', 'core', or 'attach')", mode)
	}

	if mode == "attach" {
		if params.ProcessID == 0 {
			return "", "", fmt.Errorf("processId is required for attach mode")
		}
	} else if mode == "core" {
		if params.CoreFilePath == "" {
			return "", "", fmt.Errorf("coreFilePath is required for core mode")
		}
	} else {
		if params.Path == "" {
			return "", "", fmt.Errorf("path is required for %s mode", mode)
		}
	}

	backend, err := newBackend(params)
	if err != nil {
		return "", "", err
	}
	ds.backend = backend

	if params.ToolLog != "" && params.Debugger == "delve" {
		return "", "", fmt.Errorf("tool-level logging (toolLog) is not supported for Delve; use gdb for tool-level logging")
	}

	if mode == "core" && params.Path == "" {
		if _, isGDB := ds.backend.(*debugadapters.GDBBackend); !isGDB {
			return "", "", fmt.Errorf("path is required for core mode with this debugger (only GDB can auto-detect the executable from a core file)")
		}
	}
	return port, mode, nil
}

func (ds *debuggerSession) spawnAndConnect(port, protocolLog string) error {
	cmd, listenAddr, err := ds.backend.Spawn(port, ds.logWriter)
	if err != nil {
		return err
	}
	ds.cmd = cmd

	switch ds.backend.TransportMode() {
	case "tcp":
		client, err := newDAPClient(listenAddr)
		if err != nil {
			return err
		}
		ds.client = client
	case "stdio":
		stdout, stdin := ds.backend.StdioPipes()
		if stdout == nil {
			return fmt.Errorf("stdio transport not available for %T backend", ds.backend)
		}
		ds.client = newDAPClientFromRWC(&readWriteCloser{
			Reader:      stdout,
			WriteCloser: stdin,
		})
	default:
		return fmt.Errorf("unsupported transport mode: %s", ds.backend.TransportMode())
	}

	if protocolLog != "" {
		f, err := os.Create(protocolLog)
		if err != nil {
			return fmt.Errorf("unable to open protocol log file: %w", err)
		}
		ds.protocolLogFile = f
		ds.client.SetProtocolLogger(f)
	}
	return nil
}

func (ds *debuggerSession) startSession(params DebugParams, mode string) error {
	caps, err := ds.client.InitializeRequest(ds.backend.AdapterID())
	if err != nil {
		return err
	}
	ds.capabilities = caps

	ds.launchMode = mode
	ds.programPath = params.Path
	ds.programArgs = params.Args
	ds.coreFilePath = params.CoreFilePath

	stopOnEntry := params.StopOnEntry || len(params.Breakpoints) == 0
	switch mode {
	case "source", "binary":
		launchArgs, err := ds.backend.LaunchArgs(mode, params.Path, stopOnEntry, params.Args)
		if err != nil {
			return err
		}
		req := ds.client.newRequest("launch")
		request := &dap.LaunchRequest{Request: *req}
		request.Arguments = toRawMessage(launchArgs)
		if err := ds.client.send(request); err != nil {
			return err
		}
	case "core":
		coreArgs, err := ds.backend.CoreArgs(params.Path, params.CoreFilePath)
		if err != nil {
			return err
		}
		rawArgs := toRawMessage(coreArgs)
		var request dap.Message
		if ds.backend.CoreRequestType() == "attach" {
			req := ds.client.newRequest("attach")
			request = &dap.AttachRequest{Request: *req, Arguments: rawArgs}
		} else if ds.backend.CoreRequestType() == "launch" {
			req := ds.client.newRequest("launch")
			request = &dap.LaunchRequest{Request: *req, Arguments: rawArgs}
		} else {
			return fmt.Errorf("unsupported core request type: %s", ds.backend.CoreRequestType())
		}
		if err := ds.client.send(request); err != nil {
			return err
		}
	case "attach":
		attachArgs, err := ds.backend.AttachArgs(params.ProcessID)
		if err != nil {
			return err
		}
		req := ds.client.newRequest("attach")
		request := &dap.AttachRequest{Request: *req}
		request.Arguments = toRawMessage(attachArgs)
		if err := ds.client.send(request); err != nil {
			return err
		}
	}
	return nil
}

func (ds *debuggerSession) waitForInitialized() error {
	for {
		msg, err := ds.client.ReadMessage()
		if err != nil {
			return err
		}
		switch resp := msg.(type) {
		case dap.ResponseMessage:
			if !resp.GetResponse().Success {
				return fmt.Errorf("unable to start debug session: %s", resp.GetResponse().Message)
			}
		case *dap.InitializedEvent:
			return nil
		}
	}
}

func (ds *debuggerSession) configureSession(breakpoints []BreakpointSpec) error {
	for _, bp := range breakpoints {
		if bp.Function != "" {
			seq, err := ds.client.SetFunctionBreakpointsRequest([]string{bp.Function})
			if err != nil {
				return err
			}
			if err := readAndValidateResponse(ds.client, seq, "unable to set function breakpoint"); err != nil {
				return err
			}
		} else if bp.File != "" && bp.Line > 0 {
			seq, err := ds.client.SetBreakpointsRequest(bp.File, []int{bp.Line})
			if err != nil {
				return err
			}
			if err := readAndValidateResponse(ds.client, seq, "unable to set breakpoint"); err != nil {
				return err
			}
		}
	}

	if ds.capabilities.SupportsConfigurationDoneRequest {
		configSeq, err := ds.client.ConfigurationDoneRequest()
		if err != nil {
			return err
		}
		if err := readAndValidateResponse(ds.client, configSeq, "unable to complete configuration"); err != nil {
			return err
		}
	}
	return nil
}

func (ds *debuggerSession) handleFirstStop(params DebugParams, mode string) (*mcp.CallToolResult, any, error) {
	if mode == "core" {
		var outputBuf strings.Builder
		var outputLines int
		for {
			msg, err := ds.client.ReadMessage()
			if err != nil {
				return nil, nil, err
			}
			switch ev := msg.(type) {
			case *dap.OutputEvent:
				if params.IncludeOutput {
					outputLines = appendOutputEvent(&outputBuf, ev, outputLines)
				}
				continue
			case *dap.StoppedEvent:
				ds.stoppedThreadID = ev.Body.ThreadId
				ds.lastHitBreakpointIds = ev.Body.HitBreakpointIds
				if ds.stoppedThreadID == 0 {
					ds.stoppedThreadID = 1
				}
				result, err := ds.getFullContext(ds.stoppedThreadID, 0, 20)
				if outputBuf.Len() > 0 {
					result = prependOutputToResult(result, outputBuf.String())
				}
				if err != nil || params.FullContext {
					return result, nil, err
				}
				return stopSummary(result, ev.Body.Reason, ev.Body.HitBreakpointIds), nil, nil
			case dap.EventMessage:
				continue
			}
		}
	}

	if len(params.Breakpoints) > 0 && !params.StopOnEntry {
		var stoppedThreadID int
		var stoppedHitBreakpointIds []int
		var outputBuf strings.Builder
		var outputLines int
		for {
			msg, err := ds.client.ReadMessage()
			if err != nil {
				return nil, nil, err
			}
			switch ev := msg.(type) {
			case *dap.OutputEvent:
				if params.IncludeOutput {
					outputLines = appendOutputEvent(&outputBuf, ev, outputLines)
				}
				continue
			case *dap.StoppedEvent:
				if ev.Body.Reason == "entry" {
					if _, err := ds.client.ContinueRequest(ev.Body.ThreadId); err != nil {
						return nil, nil, err
					}
					continue
				}
				stoppedThreadID = ev.Body.ThreadId
				stoppedHitBreakpointIds = ev.Body.HitBreakpointIds
				ds.stoppedThreadID = stoppedThreadID
				ds.lastHitBreakpointIds = stoppedHitBreakpointIds
				goto stopped
			case *dap.TerminatedEvent:
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "Program terminated before reaching breakpoint"}},
				}, nil, nil
			}
		}
	stopped:
		if stoppedThreadID == 0 {
			stoppedThreadID = 1
		}
		result, err := ds.getFullContext(stoppedThreadID, 0, 20)
		if outputBuf.Len() > 0 {
			result = prependOutputToResult(result, outputBuf.String())
		}
		if err != nil || params.FullContext {
			return result, nil, err
		}
		return stopSummary(result, "breakpoint", stoppedHitBreakpointIds), nil, nil
	}

	msg, err := ds.client.ReadMessage()
	if err != nil {
		return nil, nil, err
	}
	switch ev := msg.(type) {
	case *dap.StoppedEvent:
		ds.stoppedThreadID = ev.Body.ThreadId
	case *dap.TerminatedEvent:
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Program terminated"}},
		}, nil, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Debug session started for %s. Use 'breakpoint' to set breakpoints and 'continue' to run.", params.Path)}},
	}, nil, nil
}

// context returns the full debugging context at the current location.
func (ds *debuggerSession) context(ctx context.Context, _ *mcp.CallToolRequest, params ContextParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	threadID := params.ThreadID.Int()
	if threadID == 0 {
		threadID = ds.defaultThreadID()
	}
	maxFrames := params.MaxFrames.Int()
	if maxFrames == 0 {
		maxFrames = 20
	}
	result, err := ds.getFullContext(threadID, params.FrameID.Int(), maxFrames)
	if err != nil {
		// If the thread ID was invalid, try to help by listing available threads
		if strings.Contains(err.Error(), "threadId") {
			threadList := ds.getThreadList()
			if threadList != "" {
				return nil, nil, fmt.Errorf("%w\n\nAvailable threads (use info tool with type 'threads' to refresh):\n%s", err, threadList)
			}
		}
		return nil, nil, err
	}
	return result, nil, nil
}

// step executes a step command and returns the full context at the new location.
func (ds *debuggerSession) step(ctx context.Context, _ *mcp.CallToolRequest, params StepParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.client == nil {
		return nil, nil, fmt.Errorf("debugger not started")
	}

	threadID := params.ThreadID.Int()
	if threadID == 0 {
		threadID = ds.defaultThreadID()
	}

	// Execute the appropriate step command
	var stepSeq int
	switch params.Mode {
	case "over":
		seq, err := ds.client.NextRequest(threadID)
		if err != nil {
			return nil, nil, err
		}
		stepSeq = seq
	case "in":
		seq, err := ds.client.StepInRequest(threadID)
		if err != nil {
			return nil, nil, err
		}
		stepSeq = seq
	case "out":
		seq, err := ds.client.StepOutRequest(threadID)
		if err != nil {
			return nil, nil, err
		}
		stepSeq = seq
	default:
		return nil, nil, fmt.Errorf("invalid step mode: %s (must be 'over', 'in', or 'out')", params.Mode)
	}

	// Wait for stopped or terminated event
	var outputBuf strings.Builder
	var outputLines int
	for {
		msg, err := ds.client.ReadMessage()
		if err != nil {
			return nil, nil, err
		}
		switch resp := msg.(type) {
		case dap.ResponseMessage:
			r := resp.GetResponse()
			if r.RequestSeq != stepSeq {
				log.Printf("step: skipping out-of-order response (request_seq=%d, waiting for %d)", r.RequestSeq, stepSeq)
				continue
			}
			if !r.Success {
				return nil, nil, fmt.Errorf("step failed: %s", r.Message)
			}
		case *dap.OutputEvent:
			if params.IncludeOutput {
				outputLines = appendOutputEvent(&outputBuf, resp, outputLines)
			}
			continue
		case *dap.StoppedEvent:
			ds.stoppedThreadID = resp.Body.ThreadId
			ds.lastHitBreakpointIds = resp.Body.HitBreakpointIds
			result, err := ds.getFullContext(resp.Body.ThreadId, 0, 20)
			if outputBuf.Len() > 0 {
				result = prependOutputToResult(result, outputBuf.String())
			}
			if err != nil || params.FullContext {
				return result, nil, err
			}
			return stopSummary(result, resp.Body.Reason, resp.Body.HitBreakpointIds), nil, nil
		case *dap.TerminatedEvent:
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "Program terminated"}},
			}, nil, nil
		}
	}
}

// breakpoint sets a breakpoint at the specified location.
func (ds *debuggerSession) breakpoint(ctx context.Context, _ *mcp.CallToolRequest, params BreakpointToolParams) (*mcp.CallToolResult, any, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.client == nil {
		return nil, nil, fmt.Errorf("debugger not started")
	}

	if params.Function != "" {
		seq, err := ds.client.SetFunctionBreakpointsRequest([]string{params.Function})
		if err != nil {
			return nil, nil, err
		}
		if err := readAndValidateResponse(ds.client, seq, "unable to set function breakpoint"); err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Breakpoint set on function: %s", params.Function)}},
		}, nil, nil
	}

	if params.File == "" || params.Line.Int() == 0 {
		return nil, nil, fmt.Errorf("either function or file+line is required")
	}

	bpSeq, err := ds.client.SetBreakpointsRequest(params.File, []int{params.Line.Int()})
	if err != nil {
		return nil, nil, err
	}

	resp, err := readTypedResponse[*dap.SetBreakpointsResponse](ds.client, bpSeq)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to set breakpoint: %w", err)
	}
	if len(resp.Body.Breakpoints) == 0 {
		return nil, nil, fmt.Errorf("no breakpoints returned")
	}
	bp := resp.Body.Breakpoints[0]
	if !bp.Verified {
		return nil, nil, fmt.Errorf("breakpoint not verified: %s", bp.Message)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Breakpoint %d set at %s:%d", bp.Id, params.File, bp.Line)}},
	}, nil, nil
}
