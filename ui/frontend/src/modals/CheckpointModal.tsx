import { useEffect, useState } from "react";
import { Modal } from "../components/Modal";
import { toast } from "../components/Toast";
import { runtimeClient } from "../client/transport";

export function CheckpointModal({
  open,
  onClose,
  onCheckpointed,
  sourceUid,
}: {
  open: boolean;
  onClose: () => void;
  onCheckpointed: () => void;
  sourceUid: string | null;
}) {
  const [uid, setUid] = useState("");
  const [name, setName] = useState("");

  useEffect(() => {
    if (open) setUid(sourceUid ?? "");
  }, [open, sourceUid]);

  function close() {
    setUid("");
    setName("");
    onClose();
  }

  async function submit() {
    if (!sourceUid) return;
    const ref = uid.trim() || sourceUid;
    try {
      await runtimeClient.checkpointSandbox({ sandboxId: sourceUid, uid: ref, name: name.trim() });
      toast(`Checkpoint ${ref} created from ${sourceUid}`);
      onCheckpointed();
      close();
    } catch (err) {
      toast(`Checkpoint failed: ${(err as Error).message}`, true);
    }
  }

  return (
    <Modal
      open={open}
      title="Checkpoint sandbox"
      onClose={close}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={close}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary">
            Checkpoint
          </button>
        </>
      }
    >
      <div className="field">
        <label htmlFor="f-ckpt-uid">Checkpoint id</label>
        <input id="f-ckpt-uid" type="text" value={uid} onChange={(e) => setUid(e.target.value)} />
        <div className="hint">Its own storage key, returned as checkpoint_ref. Defaults to the sandbox id.</div>
      </div>
      <div className="field">
        <label htmlFor="f-ckpt-name">
          Name <span style={{ fontWeight: 400, color: "var(--text-mute)" }}>(optional)</span>
        </label>
        <input
          id="f-ckpt-name"
          type="text"
          placeholder="e.g. pre-deploy snapshot"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </div>
    </Modal>
  );
}
