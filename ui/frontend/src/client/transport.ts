import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import {
  RuntimeService,
  RootFSService,
  NetworkService,
} from "../gen/pkg/api/runtime/v1/api_pb";
import { ExecService } from "../gen/ui/proto/console/v1/exec_pb";

// baseUrl is always same-origin: in production the Go binary serves
// both the Connect API and this static build from one port, and in
// dev Vite's own proxy (vite.config.ts) forwards Connect calls to the
// backend server-side -- so the browser never needs a cross-origin
// baseUrl, and CORS is never a concern.
const transport = createConnectTransport({ baseUrl: "" });

export const runtimeClient = createClient(RuntimeService, transport);
export const rootFSClient = createClient(RootFSService, transport);
export const networkClient = createClient(NetworkService, transport);
export const execClient = createClient(ExecService, transport);
