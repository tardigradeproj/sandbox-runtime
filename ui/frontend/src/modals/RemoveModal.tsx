import { useState } from "react";
import { Modal } from "../components/Modal";
import { toast } from "../components/Toast";
import { runtimeClient } from "../client/transport";

export function RemoveModal({
  open,
  onClose,
  onRemoved,
  targetUid,
}: {
  open: boolean;
  onClose: () => void;
  onRemoved: () => void;
  targetUid: string | null;
}) {
  const [timeoutSeconds, setTimeoutSeconds] = useState("0");
  const [force, setForce] = useState(false);

  function close() {
    setTimeoutSeconds("0");
    setForce(false);
    onClose();
  }

  async function submit() {
    if (!targetUid) return;
    try {
      await runtimeClient.removeSandbox({
        sandboxId: targetUid,
        force,
        timeoutSeconds: BigInt(Math.max(0, Math.round(Number(timeoutSeconds)))),
      });
      toast(`Removing ${targetUid}…`);
      onRemoved();
      close();
    } catch (err) {
      toast(`Remove sandbox failed: ${(err as Error).message}`, true);
    }
  }

  return (
    <Modal
      open={open}
      title="Remove sandbox"
      onClose={close}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={close}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary" style={{ background: "var(--critical)" }}>
            Remove sandbox
          </button>
        </>
      }
    >
      <p style={{ fontSize: 13, margin: "0 0 16px" }}>
        Remove <strong className="mono">{targetUid}</strong>? This asks the guest to shut down
        gracefully, then releases what CreateSandbox allocated for it. Its network attachment, if
        any, is left in place — see DetachNetwork.
      </p>
      <div className="field-row">
        <div className="field">
          <label htmlFor="f-remove-timeout">Timeout (seconds)</label>
          <input
            id="f-remove-timeout"
            type="number"
            min="0"
            step="1"
            value={timeoutSeconds}
            onChange={(e) => setTimeoutSeconds(e.target.value)}
          />
        </div>
      </div>
      <div className="field">
        <label style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 500 }}>
          <input
            type="checkbox"
            style={{ width: "auto" }}
            checked={force}
            onChange={(e) => setForce(e.target.checked)}
          />
          Force — kill the process if it's still running once the timeout elapses
        </label>
      </div>
    </Modal>
  );
}
