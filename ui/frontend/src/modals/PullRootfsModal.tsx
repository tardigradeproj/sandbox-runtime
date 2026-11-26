import { useState } from "react";
import { Modal } from "../components/Modal";
import { toast } from "../components/Toast";
import { parseKVLines } from "../lib/kv";
import { rootFSClient } from "../client/transport";

export function PullRootfsModal({
  open,
  onClose,
  onPulled,
}: {
  open: boolean;
  onClose: () => void;
  onPulled: () => void;
}) {
  const [image, setImage] = useState("");
  const [annotationsText, setAnnotationsText] = useState("");

  function close() {
    setImage("");
    setAnnotationsText("");
    onClose();
  }

  async function submit() {
    if (!image.trim()) return;
    const annotations = parseKVLines(annotationsText);
    if (annotations.bad.length) {
      toast(`Annotations must be key=value: "${annotations.bad[0]}"`, true);
      return;
    }
    try {
      await rootFSClient.pullRootFS({ spec: { image: image.trim(), annotations: annotations.map } });
      toast(`Pulling ${image}…`);
      onPulled();
      close();
    } catch (err) {
      toast(`Pull rootfs failed: ${(err as Error).message}`, true);
    }
  }

  return (
    <Modal
      open={open}
      title="Pull rootfs"
      onClose={close}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={close}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary">
            Pull
          </button>
        </>
      }
    >
      <div className="field">
        <label htmlFor="f-pull-image">Image reference</label>
        <input
          id="f-pull-image"
          type="text"
          placeholder="docker.io/library/alpine:3.19"
          required
          value={image}
          onChange={(e) => setImage(e.target.value)}
        />
        <div className="hint">Fetched from an OCI registry or template catalog and stored locally.</div>
      </div>
      <div className="field">
        <label htmlFor="f-pull-annotations">
          Annotations <span className="muted">(optional, one key=value per line)</span>
        </label>
        <textarea
          className="ta"
          id="f-pull-annotations"
          rows={2}
          value={annotationsText}
          onChange={(e) => setAnnotationsText(e.target.value)}
        />
        <div className="hint">For runtimes that key rootfs resolution on more than just the reference.</div>
      </div>
    </Modal>
  );
}
