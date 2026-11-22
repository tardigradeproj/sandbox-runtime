import { useState } from "react";
import { Modal } from "../components/Modal";
import { toast } from "../components/Toast";
import { networkClient } from "../client/transport";
import { setAttachment } from "../features/network/store";

export function AttachNetworkModal({
  open,
  onClose,
  prefillUid,
  knownUids,
}: {
  open: boolean;
  onClose: () => void;
  prefillUid?: string;
  knownUids: string[];
}) {
  const [sandboxId, setSandboxId] = useState(prefillUid ?? "");

  function close() {
    setSandboxId("");
    onClose();
  }

  async function submit() {
    if (!sandboxId.trim()) return;
    try {
      const resp = await networkClient.attachNetwork({ sandboxId: sandboxId.trim() });
      setAttachment({
        sandboxId: sandboxId.trim(),
        netnsPath: resp.netnsPath,
        tapDevice: resp.tapDevice,
        ipAddress: resp.ipAddress,
        gatewayIp: resp.gatewayIp,
      });
      toast(`Network attached for ${sandboxId}`);
      close();
    } catch (err) {
      toast(`Attach network failed: ${(err as Error).message}`, true);
    }
  }

  return (
    <Modal
      open={open}
      title="Attach network"
      onClose={close}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={close}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary">
            Attach network
          </button>
        </>
      }
    >
      <div className="field">
        <label htmlFor="f-net-uid">Sandbox ID</label>
        <input
          id="f-net-uid"
          type="text"
          list="sandbox-uid-list"
          placeholder="e.g. agent-fleet-7b2c"
          required
          value={sandboxId}
          onChange={(e) => setSandboxId(e.target.value)}
        />
        <datalist id="sandbox-uid-list">
          {knownUids.map((uid) => (
            <option key={uid} value={uid} />
          ))}
        </datalist>
        <div className="hint">
          Provisions a namespace, tap device, and IP for this sandbox id. Calling it again for an
          already-attached id just returns the existing handle.
        </div>
      </div>
    </Modal>
  );
}
