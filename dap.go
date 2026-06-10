package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"

	"github.com/google/go-dap"
)

// readWriteCloser combines separate reader and writer into io.ReadWriteCloser.
type readWriteCloser struct {
	io.Reader
	io.WriteCloser
}

// DAPClient is the interface for communicating with a Debug Adapter Protocol server.
type DAPClient interface {
	Close()
	SetProtocolLogger(w io.Writer)
	ReadMessage() (dap.Message, error)
	ReadMessageWithContext(ctx context.Context) (dap.Message, error)
	InitializeRequest(ctx context.Context, adapterID string) (dap.Capabilities, error)
	LaunchRequest(arguments json.RawMessage) (int, error)
	AttachRequest(arguments json.RawMessage) (int, error)
	SetBreakpointsRequest(file string, lines []int) (int, error)
	SetFunctionBreakpointsRequest(functions []string) (int, error)
	ConfigurationDoneRequest() (int, error)
	ContinueRequest(threadID int) (int, error)
	NextRequest(threadID int) (int, error)
	StepInRequest(threadID int) (int, error)
	StepOutRequest(threadID int) (int, error)
	PauseRequest(threadID int) (int, error)
	ThreadsRequest() (int, error)
	StackTraceRequest(threadID, startFrame, levels int) (int, error)
	ScopesRequest(frameID int) (int, error)
	VariablesRequest(variablesReference int) (int, error)
	EvaluateRequest(expression string, frameID int, context string) (int, error)
	DisconnectRequest(terminateDebuggee bool) (int, error)
	SetVariableRequest(variablesRef int, name, value string) (int, error)
	RestartRequest(arguments map[string]any) (int, error)
	LoadedSourcesRequest() (int, error)
	ModulesRequest() (int, error)
	DisassembleRequest(memoryReference string, instructionOffset, instructionCount int) (int, error)
	CancelRequest(requestId int) (int, error)
}

// dapClient is a synchronous Debug Adapter Protocol client.
// It manages a connection to a DAP server and provides methods for
// sending each DAP request type. Each request method returns the
// sequence number of the sent request, which callers use to match
// the corresponding response via request_seq.
type dapClient struct {
	rwc       io.ReadWriteCloser
	reader    *bufio.Reader
	logWriter io.Writer
	seq       int
}

// newDAPClient creates a new Client over a TCP connection.
func newDAPClient(addr string) (*dapClient, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connecting to DAP server at %s: %w", addr, err)
	}
	return newDAPClientFromRWC(conn), nil
}

// newDAPClientFromRWC creates a new Client with the given ReadWriteCloser.
func newDAPClientFromRWC(rwc io.ReadWriteCloser) *dapClient {
	return &dapClient{
		rwc:    rwc,
		reader: bufio.NewReader(rwc),
		seq:    1,
	}
}

func (c *dapClient) Close() {
	c.rwc.Close()
}

func (c *dapClient) SetProtocolLogger(w io.Writer) {
	c.logWriter = w
}

func (c *dapClient) InitializeRequest(ctx context.Context, adapterID string) (dap.Capabilities, error) {
	req := c.newRequest("initialize")
	request := &dap.InitializeRequest{Request: *req}
	request.Arguments = dap.InitializeRequestArguments{
		AdapterID:                    adapterID,
		PathFormat:                   "path",
		LinesStartAt1:                true,
		ColumnsStartAt1:              true,
		SupportsVariableType:         true,
		SupportsVariablePaging:       true,
		SupportsRunInTerminalRequest: false,
		Locale:                       "en-us",
	}
	if err := c.send(request); err != nil {
		return dap.Capabilities{}, err
	}
	for {
		msg, err := c.ReadMessageWithContext(ctx)
		if err != nil {
			return dap.Capabilities{}, err
		}
		switch resp := msg.(type) {
		case *dap.InitializeResponse:
			if !resp.Success {
				return dap.Capabilities{}, fmt.Errorf("initialize failed: %s", resp.Message)
			}
			return resp.Body, nil
		case dap.EventMessage:
			continue
		default:
			return dap.Capabilities{}, fmt.Errorf("expected InitializeResponse, got %T", msg)
		}
	}
}

func (c *dapClient) ReadMessage() (dap.Message, error) {
	msg, err := dap.ReadProtocolMessage(c.reader)
	if err != nil {
		return nil, err
	}
	if c.logWriter != nil {
		if data, merr := json.Marshal(msg); merr == nil {
			fmt.Fprintf(c.logWriter, "RECV: <<<%s>>>\n", data)
		}
	}
	return msg, nil
}

func (c *dapClient) ReadMessageWithContext(ctx context.Context) (dap.Message, error) {
	type result struct {
		msg dap.Message
		err error
	}
	ch := make(chan result, 1)
	go func() {
		msg, err := c.ReadMessage()
		ch <- result{msg, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.msg, r.err
	}
}

func (c *dapClient) newRequest(command string) *dap.Request {
	request := &dap.Request{}
	request.Type = "request"
	request.Command = command
	request.Seq = c.seq
	c.seq++
	return request
}

func (c *dapClient) logSend(data []byte) {
	if c.logWriter != nil {
		fmt.Fprintf(c.logWriter, "SENT: <<<%s>>>\n", data)
	}
}

func (c *dapClient) send(request dap.Message) error {
	if data, err := json.Marshal(request); err == nil {
		c.logSend(data)
	}
	return dap.WriteProtocolMessage(c.rwc, request)
}

func toRawMessage(in any) json.RawMessage {
	out, _ := json.Marshal(in)
	return out
}

func (c *dapClient) LaunchRequest(arguments json.RawMessage) (int, error) {
	req := c.newRequest("launch")
	request := &dap.LaunchRequest{Request: *req}
	request.Arguments = arguments
	return req.Seq, c.send(request)
}

func (c *dapClient) AttachRequest(arguments json.RawMessage) (int, error) {
	req := c.newRequest("attach")
	request := &dap.AttachRequest{Request: *req}
	request.Arguments = arguments
	return req.Seq, c.send(request)
}

func (c *dapClient) CancelRequest(requestId int) (int, error) {
	req := c.newRequest("cancel")
	args := map[string]any{
		"requestId": requestId,
	}
	msg := struct {
		dap.Request
		Arguments map[string]any `json:"arguments"`
	}{Request: *req, Arguments: args}
	if data, err := json.Marshal(&msg); err == nil {
		c.logSend(data)
	}
	return req.Seq, dap.WriteProtocolMessage(c.rwc, &msg)
}

func (c *dapClient) SetBreakpointsRequest(file string, lines []int) (int, error) {
	req := c.newRequest("setBreakpoints")
	request := &dap.SetBreakpointsRequest{Request: *req}
	request.Arguments = dap.SetBreakpointsArguments{
		Source: dap.Source{
			Name: filepath.Base(file),
			Path: file,
		},
		Breakpoints: make([]dap.SourceBreakpoint, len(lines)),
	}
	for i, l := range lines {
		request.Arguments.Breakpoints[i].Line = l
	}
	return req.Seq, c.send(request)
}

func (c *dapClient) SetFunctionBreakpointsRequest(functions []string) (int, error) {
	req := c.newRequest("setFunctionBreakpoints")
	request := &dap.SetFunctionBreakpointsRequest{Request: *req}
	request.Arguments = dap.SetFunctionBreakpointsArguments{
		Breakpoints: make([]dap.FunctionBreakpoint, len(functions)),
	}
	for i, f := range functions {
		request.Arguments.Breakpoints[i].Name = f
	}
	return req.Seq, c.send(request)
}

func (c *dapClient) ConfigurationDoneRequest() (int, error) {
	req := c.newRequest("configurationDone")
	request := &dap.ConfigurationDoneRequest{Request: *req}
	return req.Seq, c.send(request)
}

func (c *dapClient) ContinueRequest(threadID int) (int, error) {
	req := c.newRequest("continue")
	request := &dap.ContinueRequest{Request: *req}
	request.Arguments.ThreadId = threadID
	return req.Seq, c.send(request)
}

func (c *dapClient) NextRequest(threadID int) (int, error) {
	req := c.newRequest("next")
	request := &dap.NextRequest{Request: *req}
	request.Arguments.ThreadId = threadID
	return req.Seq, c.send(request)
}

func (c *dapClient) StepInRequest(threadID int) (int, error) {
	req := c.newRequest("stepIn")
	request := &dap.StepInRequest{Request: *req}
	request.Arguments.ThreadId = threadID
	return req.Seq, c.send(request)
}

func (c *dapClient) StepOutRequest(threadID int) (int, error) {
	req := c.newRequest("stepOut")
	request := &dap.StepOutRequest{Request: *req}
	request.Arguments.ThreadId = threadID
	return req.Seq, c.send(request)
}

func (c *dapClient) PauseRequest(threadID int) (int, error) {
	req := c.newRequest("pause")
	request := &dap.PauseRequest{Request: *req}
	request.Arguments.ThreadId = threadID
	return req.Seq, c.send(request)
}

func (c *dapClient) ThreadsRequest() (int, error) {
	req := c.newRequest("threads")
	request := &dap.ThreadsRequest{Request: *req}
	return req.Seq, c.send(request)
}

func (c *dapClient) StackTraceRequest(threadID, startFrame, levels int) (int, error) {
	req := c.newRequest("stackTrace")
	request := &dap.StackTraceRequest{Request: *req}
	request.Arguments.ThreadId = threadID
	request.Arguments.StartFrame = startFrame
	request.Arguments.Levels = levels
	return req.Seq, c.send(request)
}

func (c *dapClient) ScopesRequest(frameID int) (int, error) {
	req := c.newRequest("scopes")
	request := &dap.ScopesRequest{Request: *req}
	request.Arguments.FrameId = frameID
	return req.Seq, c.send(request)
}

func (c *dapClient) VariablesRequest(variablesReference int) (int, error) {
	req := c.newRequest("variables")
	request := &dap.VariablesRequest{Request: *req}
	request.Arguments.VariablesReference = variablesReference
	return req.Seq, c.send(request)
}

func (c *dapClient) EvaluateRequest(expression string, frameID int, context string) (int, error) {
	req := c.newRequest("evaluate")
	args := map[string]any{
		"expression": expression,
		"frameId":    frameID,
	}
	if context != "" {
		args["context"] = context
	}
	msg := struct {
		dap.Request
		Arguments map[string]any `json:"arguments"`
	}{Request: *req, Arguments: args}
	if data, err := json.Marshal(&msg); err == nil {
		c.logSend(data)
	}
	return req.Seq, dap.WriteProtocolMessage(c.rwc, &msg)
}

func (c *dapClient) DisconnectRequest(terminateDebuggee bool) (int, error) {
	req := c.newRequest("disconnect")
	request := &dap.DisconnectRequest{Request: *req}
	request.Arguments = &dap.DisconnectArguments{
		TerminateDebuggee: terminateDebuggee,
	}
	return req.Seq, c.send(request)
}

func (c *dapClient) SetVariableRequest(variablesRef int, name, value string) (int, error) {
	req := c.newRequest("setVariable")
	request := &dap.SetVariableRequest{Request: *req}
	request.Arguments.VariablesReference = variablesRef
	request.Arguments.Name = name
	request.Arguments.Value = value
	return req.Seq, c.send(request)
}

func (c *dapClient) RestartRequest(arguments map[string]any) (int, error) {
	req := c.newRequest("restart")
	request := &dap.RestartRequest{Request: *req}
	if arguments != nil {
		request.Arguments = toRawMessage(arguments)
	}
	return req.Seq, c.send(request)
}

func (c *dapClient) LoadedSourcesRequest() (int, error) {
	req := c.newRequest("loadedSources")
	request := &dap.LoadedSourcesRequest{Request: *req}
	return req.Seq, c.send(request)
}

func (c *dapClient) ModulesRequest() (int, error) {
	req := c.newRequest("modules")
	request := &dap.ModulesRequest{Request: *req}
	return req.Seq, c.send(request)
}

func (c *dapClient) DisassembleRequest(memoryReference string, instructionOffset, instructionCount int) (int, error) {
	req := c.newRequest("disassemble")
	request := &dap.DisassembleRequest{Request: *req}
	request.Arguments.MemoryReference = memoryReference
	request.Arguments.InstructionOffset = instructionOffset
	request.Arguments.InstructionCount = instructionCount
	return req.Seq, c.send(request)
}
