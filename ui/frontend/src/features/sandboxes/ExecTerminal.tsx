import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { execClient } from "../../client/transport";
import { Signal } from "../../gen/ui/proto/console/v1/exec_pb";

type Status = "connecting" | "connected" | "exited" | "error";

const STATUS_COLOR: Record<Status, string> = {
  connecting: "var(--warning)",
  connected: "var(--good)",
  exited: "var(--neutral-state)",
  error: "var(--critical)",
};

// ExecTerminal is a real interactive PTY, bridged by ui/server/exec.go
// straight to this sandbox's own sandboxd ProcessService at its guest
// IP -- RuntimeService itself is never in this path.
export function ExecTerminal({ address }: { address: string }) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [status, setStatus] = useState<Status>("connecting");
  const [statusText, setStatusText] = useState("connecting…");

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    setStatus("connecting");
    setStatusText("connecting…");

    const term = new Terminal({
      convertEol: true,
      fontFamily: '"JetBrains Mono", monospace',
      fontSize: 13,
      cursorBlink: true,
      theme: {
        background: "#f4f6fa",
        foreground: "#2b3542",
        cursor: "#3e5fd9",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(container);
    fit.fit();

    const abort = new AbortController();
    let processId: number | null = null;
    let exited = false;

    (async () => {
      try {
        const stream = execClient.start(
          {
            address,
            command: ["/bin/sh"],
            envVars: { TERM: "xterm-256color" },
            pty: { cols: term.cols, rows: term.rows },
          },
          { signal: abort.signal },
        );
        for await (const resp of stream) {
          switch (resp.event.case) {
            case "init":
              processId = resp.event.value.processId;
              setStatus("connected");
              setStatusText(`pid ${resp.event.value.processId}`);
              break;
            case "stdout":
            case "stderr":
              term.write(resp.event.value);
              break;
            case "exit":
              exited = true;
              term.write(`\r\n[process exited: ${resp.event.value.exitCode}]\r\n`);
              setStatus("exited");
              setStatusText(`exited (${resp.event.value.exitCode})`);
              break;
          }
        }
      } catch (err) {
        if (!abort.signal.aborted) {
          setStatus("error");
          setStatusText(err instanceof Error ? err.message : String(err));
        }
      }
    })();

    const onData = term.onData((data) => {
      if (processId === null || exited) return;
      execClient
        .writeStdin({ address, processId, payload: { case: "input", value: new TextEncoder().encode(data) } })
        .catch(() => {});
    });

    const onResize = term.onResize(({ cols, rows }) => {
      if (processId === null) return;
      execClient.resizeTTY({ address, processId, cols, rows }).catch(() => {});
    });

    const observer = new ResizeObserver(() => fit.fit());
    observer.observe(container);

    return () => {
      abort.abort();
      onData.dispose();
      onResize.dispose();
      observer.disconnect();
      if (processId !== null && !exited) {
        execClient.sendSignal({ address, processId, signal: Signal.SIGTERM }).catch(() => {});
      }
      term.dispose();
    };
  }, [address]);

  return (
    <div className="term-wrap">
      <div className="term-titlebar">
        <span className="term-dot" style={{ background: "#ff5f57" }} />
        <span className="term-dot" style={{ background: "#febc2e" }} />
        <span className="term-dot" style={{ background: "#28c840" }} />
        <span className="label">/bin/sh — {address}</span>
        <span className="term-status">
          <span className="dot" style={{ background: STATUS_COLOR[status] }} />
          {statusText}
        </span>
      </div>
      <div className="term-body" style={{ padding: 0 }} ref={containerRef} />
    </div>
  );
}
