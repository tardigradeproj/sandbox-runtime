import { useState } from "react";
import type { SandboxTrait } from "../../gen/pkg/api/runtime/v1/api_pb";
import { SandboxStateValue } from "../../gen/pkg/api/runtime/v1/api_pb";
import { StatusChip } from "../../components/StatusChip";
import { LabelPills } from "../../components/LabelPills";
import { CloseIcon } from "../../components/Modal";
import { toast } from "../../components/Toast";
import { runtimeClient } from "../../client/transport";
import { useNetworkAttachment } from "../network/store";
import { ExecTerminal } from "./ExecTerminal";

type Tab = "overview" | "network" | "exec";

function mib(bytes: bigint | undefined): string {
  if (bytes === undefined) return "—";
  return `${(Number(bytes) / (1024 * 1024)).toFixed(0)} MiB`;
}

export function SandboxDetailPanel({
  sandbox,
  onClose,
  onChanged,
  onCheckpoint,
  onRemove,
  onEditUserData,
  onAttachNetwork,
}: {
  sandbox: SandboxTrait | null;
  onClose: () => void;
  onChanged: () => void;
  onCheckpoint: (uid: string) => void;
  onRemove: (uid: string) => void;
  onEditUserData: (uid: string) => void;
  onAttachNetwork: (uid: string) => void;
}) {
  const [tab, setTab] = useState<Tab>("overview");
  const uid = sandbox?.config?.metadata?.uid ?? "";
  const attachment = useNetworkAttachment(uid);

  if (!sandbox) {
    return (
      <>
        <div className="scrim" />
        <div className="panel" role="dialog" aria-label="Sandbox details" />
      </>
    );
  }

  const config = sandbox.config;
  const bootSource = config?.bootSource;
  const image =
    bootSource?.case === "rootfs" ? bootSource.value.spec?.image : `from checkpoint ${bootSource?.value}`;

  async function start() {
    try {
      await runtimeClient.startSandbox({ sandboxId: uid });
      toast(`${uid} started`);
      onChanged();
    } catch (err) {
      toast(`Start sandbox failed: ${(err as Error).message}`, true);
    }
  }
  async function stop() {
    try {
      await runtimeClient.stopSandbox({ sandboxId: uid });
      toast(`${uid} stopped`);
      onChanged();
    } catch (err) {
      toast(`Stop sandbox failed: ${(err as Error).message}`, true);
    }
  }

  return (
    <>
      <div className="scrim open" onClick={onClose} />
      <div className="panel open" role="dialog" aria-label="Sandbox details">
        <div className="panel-head">
          <div>
            <div className="uid">{uid}</div>
            <div className="image">{image}</div>
          </div>
          <div className="panel-actions">
            {sandbox.state !== SandboxStateValue.STATE_RUNNING && (
              <button className="btn btn-sm" title="StartSandbox" onClick={start}>
                Start
              </button>
            )}
            {sandbox.state === SandboxStateValue.STATE_RUNNING && (
              <button className="btn btn-sm" title="StopSandbox" onClick={stop}>
                Stop
              </button>
            )}
            <button className="btn btn-sm" title="CheckpointSandbox" onClick={() => onCheckpoint(uid)}>
              Checkpoint
            </button>
            <button
              className="btn btn-sm btn-danger-ghost"
              title="RemoveSandbox"
              onClick={() => onRemove(uid)}
            >
              Remove
            </button>
            <button className="panel-close" aria-label="Close" onClick={onClose}>
              <CloseIcon />
            </button>
          </div>
        </div>

        <div className="tabs">
          <button className={`tab${tab === "overview" ? " active" : ""}`} onClick={() => setTab("overview")}>
            Overview
          </button>
          <button className={`tab${tab === "network" ? " active" : ""}`} onClick={() => setTab("network")}>
            Network
          </button>
          <button className={`tab${tab === "exec" ? " active" : ""}`} onClick={() => setTab("exec")}>
            Exec
          </button>
        </div>

        {tab === "overview" && (
          <div className="tab-panel active">
            <div className="tab-body">
              <div className="section-label">Configuration</div>
              <table className="kv-table">
                <tbody>
                  <tr>
                    <td>Name</td>
                    <td>{config?.metadata?.name || "—"}</td>
                  </tr>
                  <tr>
                    <td>Status</td>
                    <td>
                      <StatusChip state={sandbox.state} />
                    </td>
                  </tr>
                  <tr>
                    <td>vCPU</td>
                    <td>{config?.resources?.cpu ?? "—"}</td>
                  </tr>
                  <tr>
                    <td>Memory</td>
                    <td>{mib(config?.resources?.memoryBytes)}</td>
                  </tr>
                  <tr>
                    <td>Storage</td>
                    <td>{mib(config?.resources?.storageBytes)}</td>
                  </tr>
                  <tr>
                    <td>Boot args</td>
                    <td className="mono">{config?.boot?.bootArgs || "—"}</td>
                  </tr>
                  {bootSource?.case === "rootfs" && (
                    <>
                      <tr>
                        <td>Read-only</td>
                        <td>{bootSource.value.isReadOnly ? "true" : "false"}</td>
                      </tr>
                      <tr>
                        <td>Partuuid</td>
                        <td className="mono">{bootSource.value.partuuid || "—"}</td>
                      </tr>
                      <tr>
                        <td>Kernel image</td>
                        <td className="mono">{config?.boot?.kernelImagePath || "— (runtime default)"}</td>
                      </tr>
                      <tr>
                        <td>Initrd</td>
                        <td className="mono">{config?.boot?.initrdPath || "—"}</td>
                      </tr>
                    </>
                  )}
                </tbody>
              </table>

              <div className="section-label">Labels</div>
              <LabelPills labels={config?.metadata?.labels} />

              <div
                className="section-label"
                style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}
              >
                <span>User data</span>
                <button
                  type="button"
                  className="btn btn-sm"
                  style={{ margin: 0 }}
                  onClick={() => onEditUserData(uid)}
                >
                  Edit user data
                </button>
              </div>
              <div style={{ marginBottom: 10 }}>
                <div className="hint" style={{ margin: "0 0 4px" }}>
                  Boot script
                </div>
                {config?.userData?.boot ? (
                  <div className="data-preview">{config.userData.boot}</div>
                ) : (
                  <div className="data-preview is-empty">No boot script set.</div>
                )}
              </div>
              <div>
                <div className="hint" style={{ margin: "0 0 4px" }}>
                  System
                </div>
                {config?.userData?.system ? (
                  <div className="data-preview">{JSON.stringify(config.userData.system, null, 2)}</div>
                ) : (
                  <div className="data-preview is-empty">No system data set.</div>
                )}
              </div>
            </div>
          </div>
        )}

        {tab === "network" && (
          <div className="tab-panel active">
            <div className="tab-body">
              {attachment ? (
                <table className="kv-table">
                  <tbody>
                    <tr>
                      <td>Namespace</td>
                      <td className="mono">{attachment.netnsPath}</td>
                    </tr>
                    <tr>
                      <td>Tap device</td>
                      <td className="mono">{attachment.tapDevice}</td>
                    </tr>
                    <tr>
                      <td>Address</td>
                      <td className="mono">{attachment.ipAddress}</td>
                    </tr>
                    <tr>
                      <td>Gateway</td>
                      <td className="mono">{attachment.gatewayIp}</td>
                    </tr>
                  </tbody>
                </table>
              ) : (
                <div className="empty-state">
                  <p style={{ marginBottom: 12 }}>No network attached (in this session).</p>
                  <button className="btn btn-primary btn-sm" onClick={() => onAttachNetwork(uid)}>
                    Attach network
                  </button>
                </div>
              )}
            </div>
          </div>
        )}

        {tab === "exec" && (
          <div className="tab-panel active">
            {sandbox.network?.ipAddress ? (
              // ipAddress is a CIDR string (e.g. "10.200.5.68/24") for
              // display elsewhere; ExecTerminal needs a bare, dialable
              // IP to hand to ui/server's grpc.NewClient.
              <ExecTerminal address={sandbox.network.ipAddress.split("/")[0]} />
            ) : (
              <div className="tab-body">
                <div className="empty-state">
                  <p style={{ marginBottom: 12 }}>
                    Attach network first — sandboxd's <span className="mono">ProcessService</span> is reachable
                    directly at the sandbox's guest IP once it has one.
                  </p>
                  <button className="btn btn-primary btn-sm" onClick={() => onAttachNetwork(uid)}>
                    Attach network
                  </button>
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </>
  );
}
