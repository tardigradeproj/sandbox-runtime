import { useEffect, useState } from "react";
import { Modal } from "../components/Modal";
import { toast } from "../components/Toast";
import { runtimeClient } from "../client/transport";
import type { SandboxUserData } from "../gen/pkg/api/runtime/v1/api_pb";
import type { JsonObject } from "@bufbuild/protobuf";

export function UpdateUserDataModal({
  open,
  onClose,
  onUpdated,
  targetUid,
  current,
}: {
  open: boolean;
  onClose: () => void;
  onUpdated: () => void;
  targetUid: string | null;
  current: SandboxUserData | undefined;
}) {
  const [boot, setBoot] = useState("");
  const [systemText, setSystemText] = useState("");

  useEffect(() => {
    if (!open) return;
    setBoot(current?.boot ?? "");
    setSystemText(current?.system ? JSON.stringify(current.system, null, 2) : "");
  }, [open, current]);

  async function submit() {
    if (!targetUid) return;
    let system: JsonObject | undefined;
    if (systemText.trim()) {
      try {
        system = JSON.parse(systemText);
      } catch {
        toast("System must be valid JSON", true);
        return;
      }
    }
    try {
      await runtimeClient.updateSandboxUserData({
        sandboxId: targetUid,
        userData: { boot, system },
      });
      toast(`User data updated for ${targetUid}`);
      onUpdated();
      onClose();
    } catch (err) {
      toast(`Update user data failed: ${(err as Error).message}`, true);
    }
  }

  return (
    <Modal
      open={open}
      title="Edit user data"
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary">
            Save
          </button>
        </>
      }
    >
      <div className="field">
        <label htmlFor="f-userdata-boot">Boot script</label>
        <textarea
          className="ta"
          id="f-userdata-boot"
          rows={4}
          placeholder={"#!/bin/sh\necho hello"}
          value={boot}
          onChange={(e) => setBoot(e.target.value)}
        />
        <div className="hint">Run by images/user-data-boot at guest boot, over the metadata endpoint.</div>
      </div>
      <div className="field">
        <label htmlFor="f-userdata-system">
          System <span style={{ fontWeight: 400, color: "var(--text-mute)" }}>(JSON, optional)</span>
        </label>
        <textarea
          className="ta"
          id="f-userdata-system"
          rows={3}
          placeholder='{"key": "value"}'
          value={systemText}
          onChange={(e) => setSystemText(e.target.value)}
        />
      </div>
    </Modal>
  );
}
