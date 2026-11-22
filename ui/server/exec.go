// Copyright 2026 The Tardigrade Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	connect "connectrpc.com/connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	processv1 "sigs.k8s.io/agent-sandbox/packages/sandboxd/spec/process/v1"

	v1 "github.com/tardigradeproj/sandbox-runtime/ui/proto/console/v1"
	"github.com/tardigradeproj/sandbox-runtime/ui/proto/console/v1/consolev1connect"
)

// sandboxdProcessPort is sandboxd's fixed ProcessService gRPC port (see
// the reference_sandboxd_api memory / packages/sandboxd's USER_GUIDE.md
// in the sigs.k8s.io/agent-sandbox module).
const sandboxdProcessPort = "9090"

// execHandler bridges console.v1.ExecService (browser-reachable, over
// Connect) to a sandbox's own sandboxd ProcessService, dialed directly
// at its guest IP. RuntimeService itself never proxies process
// execution into the guest (see the project_sri_scope_boundary memory),
// so this talks to sandboxd straight from ui/server, independent of
// runtime.Runtime -- not a new sri feature, a console-only bridge.
type execHandler struct {
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

func newExecHandler() *execHandler {
	return &execHandler{conns: make(map[string]*grpc.ClientConn)}
}

// client returns a cached ProcessServiceClient for address, dialing
// lazily on first use. Cached per-address rather than per-call: a live
// terminal sends a WriteStdin per keystroke, and redialing a fresh TCP
// connection on every keypress would make typing visibly laggy.
func (h *execHandler) client(address string) (processv1.ProcessServiceClient, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if conn, ok := h.conns[address]; ok {
		return processv1.NewProcessServiceClient(conn), nil
	}
	conn, err := grpc.NewClient(address+":"+sandboxdProcessPort, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial sandboxd at %s: %w", address, err)
	}
	h.conns[address] = conn
	return processv1.NewProcessServiceClient(conn), nil
}

func (h *execHandler) Start(ctx context.Context, req *connect.Request[v1.StartRequest], stream *connect.ServerStream[v1.StartResponse]) error {
	client, err := h.client(req.Msg.GetAddress())
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, err)
	}

	var pty *processv1.PTY
	if p := req.Msg.GetPty(); p != nil {
		pty = &processv1.PTY{Cols: p.GetCols(), Rows: p.GetRows()}
	}
	upstream, err := client.Start(ctx, &processv1.StartRequest{
		Config: &processv1.ProcessConfig{
			Command: req.Msg.GetCommand(),
			EnvVars: req.Msg.GetEnvVars(),
			Cwd:     req.Msg.Cwd,
		},
		Pty: pty,
	})
	if err != nil {
		return sandboxdError(err)
	}

	for {
		ev, err := upstream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return sandboxdError(err)
		}

		resp := &v1.StartResponse{}
		switch e := ev.GetEvent().(type) {
		case *processv1.StartResponse_Init:
			resp.Event = &v1.StartResponse_Init{Init: &v1.InitEvent{ProcessId: e.Init.GetProcessId()}}
		case *processv1.StartResponse_Stdout:
			resp.Event = &v1.StartResponse_Stdout{Stdout: e.Stdout}
		case *processv1.StartResponse_Stderr:
			resp.Event = &v1.StartResponse_Stderr{Stderr: e.Stderr}
		case *processv1.StartResponse_Exit:
			resp.Event = &v1.StartResponse_Exit{Exit: &v1.ExitEvent{ExitCode: e.Exit.GetExitCode()}}
		default:
			continue
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

func (h *execHandler) WriteStdin(ctx context.Context, req *connect.Request[v1.WriteStdinRequest]) (*connect.Response[v1.WriteStdinResponse], error) {
	client, err := h.client(req.Msg.GetAddress())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	out := &processv1.WriteStdinRequest{ProcessId: req.Msg.GetProcessId()}
	switch p := req.Msg.GetPayload().(type) {
	case *v1.WriteStdinRequest_Input:
		out.Payload = &processv1.WriteStdinRequest_Input{Input: p.Input}
	case *v1.WriteStdinRequest_Eof:
		out.Payload = &processv1.WriteStdinRequest_Eof{Eof: p.Eof}
	}
	if _, err := client.WriteStdin(ctx, out); err != nil {
		return nil, sandboxdError(err)
	}
	return connect.NewResponse(&v1.WriteStdinResponse{}), nil
}

func (h *execHandler) ResizeTTY(ctx context.Context, req *connect.Request[v1.ResizeTTYRequest]) (*connect.Response[v1.ResizeTTYResponse], error) {
	client, err := h.client(req.Msg.GetAddress())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	if _, err := client.ResizeTTY(ctx, &processv1.ResizeTTYRequest{
		ProcessId: req.Msg.GetProcessId(),
		Cols:      req.Msg.GetCols(),
		Rows:      req.Msg.GetRows(),
	}); err != nil {
		return nil, sandboxdError(err)
	}
	return connect.NewResponse(&v1.ResizeTTYResponse{}), nil
}

func (h *execHandler) SendSignal(ctx context.Context, req *connect.Request[v1.SendSignalRequest]) (*connect.Response[v1.SendSignalResponse], error) {
	client, err := h.client(req.Msg.GetAddress())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	if _, err := client.SendSignal(ctx, &processv1.SendSignalRequest{
		ProcessId: req.Msg.GetProcessId(),
		Signal:    processv1.Signal(req.Msg.GetSignal()),
	}); err != nil {
		return nil, sandboxdError(err)
	}
	return connect.NewResponse(&v1.SendSignalResponse{}), nil
}

var _ consolev1connect.ExecServiceHandler = (*execHandler)(nil)

// sandboxdError translates one of sandboxd's grpc/status errors into a
// connect.Error with the equivalent code -- the same translation
// connectError (handlers.go) does for sri's own service errors, needed
// here because sandboxd is a second, independent gRPC peer.
func sandboxdError(err error) error {
	if err == nil {
		return nil
	}
	s, ok := grpcstatus.FromError(err)
	if !ok {
		return connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewError(connect.Code(s.Code()), s.Err())
}
