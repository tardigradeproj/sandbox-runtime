import { useEffect, useState } from "react";
import { Modal } from "../components/Modal";
import { toast } from "../components/Toast";
import { parseKVLines } from "../lib/kv";
import { runtimeClient, networkClient } from "../client/transport";
import type { RootFS, Checkpoint } from "../gen/pkg/api/runtime/v1/api_pb";
import type { JsonObject } from "@bufbuild/protobuf";
import { setAttachment } from "../features/network/store";

type BootSource = "rootfs" | "checkpoint";

const initialState = {
  name: "",
  uid: "",
  labelsText: "",
  bootSource: "rootfs" as BootSource,
  image: "",
  cpu: "1",
  memMiB: "512",
  storageMiB: "1024",
  checkpointRef: "",
  advancedOpen: false,
  readOnly: true,
  partuuid: "",
  rootfsAnnotationsText: "",
  kernelPath: "",
  initrdPath: "",
  bootArgs: "",
  userDataBoot: "",
  userDataSystemText: "",
};

export function CreateSandboxModal({
  open,
  onClose,
  onCreated,
  images,
  checkpoints,
  initialBootSource,
}: {
  open: boolean;
  onClose: () => void;
  onCreated: () => void;
  images: RootFS[];
  checkpoints: Checkpoint[];
  // Set when opened from the Checkpoints view's "restore" row action,
  // to pre-steer straight onto that checkpoint instead of the default
  // rootfs boot source.
  initialBootSource?: { source: "checkpoint"; ref: string };
}) {
  const [f, setF] = useState(initialState);
  const set = <K extends keyof typeof initialState>(key: K, value: (typeof initialState)[K]) =>
    setF((prev) => ({ ...prev, [key]: value }));

  useEffect(() => {
    if (open && initialBootSource?.source === "checkpoint") {
      setF((prev) => ({ ...prev, bootSource: "checkpoint", checkpointRef: initialBootSource.ref }));
    }
    // Only react to the modal opening/the target checkpoint changing,
    // not to every keystroke in the rest of the form.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initialBootSource?.ref]);

  useEffect(() => {
    // The <select> visually shows its first <option> the instant
    // `images` loads, but a *controlled* <select> never syncs that
    // back into React state on its own (no change event fires) --
    // without this, f.image stays "" until the user manually reopens
    // the dropdown, and submit silently sends an empty image.
    if (open && !f.image && images.length > 0) {
      setF((prev) => (prev.image ? prev : { ...prev, image: images[0].spec?.image ?? "" }));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, images]);

  function close() {
    setF(initialState);
    onClose();
  }

  async function submit() {
    if (!f.uid.trim()) return;

    const labels = parseKVLines(f.labelsText);
    if (labels.bad.length) {
      toast(`Labels must be key=value: "${labels.bad[0]}"`, true);
      return;
    }

    let system: JsonObject | undefined;
    if (f.userDataSystemText.trim()) {
      try {
        system = JSON.parse(f.userDataSystemText);
      } catch {
        toast("User data system must be valid JSON", true);
        return;
      }
    }
    const userData =
      f.userDataBoot || system ? { boot: f.userDataBoot, system } : undefined;

    const metadata = { name: f.name.trim(), uid: f.uid.trim(), labels: labels.map };

    try {
      // CreateSandbox assumes the sandbox's namespace and tap device
      // already exist when jailer is enabled (see cmd/main_validation.go's
      // own comment to that effect, and runtime_linux.go's jailSandbox,
      // which chowns the already-attached tap device). AttachNetwork is
      // documented idempotent, so it's always safe to call first here
      // regardless of whether jailer is actually on.
      const att = await networkClient.attachNetwork({ sandboxId: f.uid.trim() });
      setAttachment({
        sandboxId: f.uid.trim(),
        netnsPath: att.netnsPath,
        tapDevice: att.tapDevice,
        ipAddress: att.ipAddress,
        gatewayIp: att.gatewayIp,
      });

      if (f.bootSource === "checkpoint") {
        if (!f.checkpointRef) {
          toast("Select a checkpoint", true);
          return;
        }
        await runtimeClient.createSandbox({
          config: {
            metadata,
            bootSource: { case: "checkpointRef", value: f.checkpointRef },
            userData,
          },
        });
      } else {
        const rootfsAnnotations = parseKVLines(f.rootfsAnnotationsText);
        if (rootfsAnnotations.bad.length) {
          toast(`Rootfs annotations must be key=value: "${rootfsAnnotations.bad[0]}"`, true);
          return;
        }
        await runtimeClient.createSandbox({
          config: {
            metadata,
            bootSource: {
              case: "rootfs",
              value: {
                spec: { image: f.image, annotations: rootfsAnnotations.map },
                isReadOnly: f.readOnly,
                partuuid: f.partuuid.trim(),
              },
            },
            boot: {
              kernelImagePath: f.kernelPath.trim(),
              initrdPath: f.initrdPath.trim(),
              bootArgs: f.bootArgs.trim(),
            },
            resources: {
              cpu: Number(f.cpu),
              memoryBytes: BigInt(Math.round(Number(f.memMiB) * 1024 * 1024)),
              storageBytes: BigInt(Math.round(Number(f.storageMiB) * 1024 * 1024)),
            },
            userData,
          },
        });
      }
      toast(`Creating ${f.uid}…`);
      onCreated();
      close();
    } catch (err) {
      toast(`Create sandbox failed: ${(err as Error).message}`, true);
    }
  }

  return (
    <Modal
      open={open}
      title="Create sandbox"
      onClose={close}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={close}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary">
            Create sandbox
          </button>
        </>
      }
    >
      <div className="section-label">Metadata</div>
      <div className="field-row">
        <div className="field">
          <label htmlFor="f-name">
            Name <span className="muted">(optional)</span>
          </label>
          <input
            id="f-name"
            type="text"
            placeholder="human-readable, need not be unique"
            value={f.name}
            onChange={(e) => set("name", e.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor="f-uid">Uid</label>
          <input
            id="f-uid"
            type="text"
            placeholder="e.g. agent-fleet-7b2c"
            required
            value={f.uid}
            onChange={(e) => set("uid", e.target.value)}
          />
        </div>
      </div>
      <div className="field">
        <label htmlFor="f-labels">
          Labels <span className="muted">(optional, one key=value per line)</span>
        </label>
        <textarea
          className="ta"
          id="f-labels"
          rows={2}
          value={f.labelsText}
          onChange={(e) => set("labelsText", e.target.value)}
        />
      </div>

      <div className="seg-control">
        <button
          type="button"
          className={`seg-btn${f.bootSource === "rootfs" ? " active" : ""}`}
          onClick={() => set("bootSource", "rootfs")}
        >
          Rootfs image
        </button>
        <button
          type="button"
          className={`seg-btn${f.bootSource === "checkpoint" ? " active" : ""}`}
          onClick={() => set("bootSource", "checkpoint")}
        >
          From checkpoint
        </button>
      </div>

      {f.bootSource === "rootfs" ? (
        <div>
          <div className="field">
            <label htmlFor="f-image">Rootfs image</label>
            <select id="f-image" value={f.image} onChange={(e) => set("image", e.target.value)}>
              {images.length === 0 && (
                <option value="" disabled>
                  No rootfs images pulled yet
                </option>
              )}
              {images.map((r) => (
                <option key={r.spec?.image ?? r.repoTags[0]} value={r.spec?.image ?? r.repoTags[0]}>
                  {r.spec?.image ?? r.repoTags[0]}
                </option>
              ))}
            </select>
            <div className="hint">Must already be pulled — see Rootfs images.</div>
          </div>
          <div className="field-row">
            <div className="field">
              <label htmlFor="f-cpu">vCPU</label>
              <input
                id="f-cpu"
                type="number"
                min="0.5"
                step="0.5"
                value={f.cpu}
                onChange={(e) => set("cpu", e.target.value)}
              />
            </div>
            <div className="field">
              <label htmlFor="f-mem">Memory (MiB)</label>
              <input
                id="f-mem"
                type="number"
                min="64"
                step="64"
                value={f.memMiB}
                onChange={(e) => set("memMiB", e.target.value)}
              />
            </div>
            <div className="field">
              <label htmlFor="f-storage">Storage (MiB)</label>
              <input
                id="f-storage"
                type="number"
                min="256"
                step="256"
                value={f.storageMiB}
                onChange={(e) => set("storageMiB", e.target.value)}
              />
            </div>
          </div>
        </div>
      ) : (
        <div className="field">
          <label htmlFor="f-checkpoint-ref">Checkpoint</label>
          <select
            id="f-checkpoint-ref"
            value={f.checkpointRef}
            onChange={(e) => set("checkpointRef", e.target.value)}
          >
            <option value="" disabled>
              {checkpoints.length ? "Select a checkpoint" : "No checkpoints yet"}
            </option>
            {checkpoints.map((c) => (
              <option key={c.checkpointRef} value={c.checkpointRef}>
                {c.checkpointRef} — from {c.config?.metadata?.uid}
              </option>
            ))}
          </select>
          <div className="hint">Boot and resources are ignored — the checkpoint already captured both.</div>
        </div>
      )}

      <button
        type="button"
        className="btn btn-sm"
        onClick={() => set("advancedOpen", !f.advancedOpen)}
      >
        Advanced options {f.advancedOpen ? "▴" : "▾"}
      </button>

      {f.advancedOpen && (
        <div style={{ marginTop: 14 }}>
          {f.bootSource === "rootfs" && (
            <div>
              <div className="section-label">Mount</div>
              <div className="field-row">
                <div className="field" style={{ flex: 1 }}>
                  <label style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 500 }}>
                    <input
                      type="checkbox"
                      style={{ width: "auto" }}
                      checked={f.readOnly}
                      onChange={(e) => set("readOnly", e.target.checked)}
                    />
                    Read-only
                  </label>
                </div>
                <div className="field" style={{ flex: 2 }}>
                  <label htmlFor="f-partuuid">
                    Partuuid <span className="muted">(optional)</span>
                  </label>
                  <input
                    id="f-partuuid"
                    type="text"
                    placeholder="takes precedence over device-index lookup"
                    value={f.partuuid}
                    onChange={(e) => set("partuuid", e.target.value)}
                  />
                </div>
              </div>
              <div className="field">
                <label htmlFor="f-rootfs-annotations">
                  Rootfs annotations <span className="muted">(optional, one key=value per line)</span>
                </label>
                <textarea
                  className="ta"
                  id="f-rootfs-annotations"
                  rows={2}
                  value={f.rootfsAnnotationsText}
                  onChange={(e) => set("rootfsAnnotationsText", e.target.value)}
                />
              </div>

              <div className="section-label">Boot</div>
              <div className="field">
                <label htmlFor="f-kernel-path">
                  Kernel image path <span className="muted">(optional, uses runtime default)</span>
                </label>
                <input
                  id="f-kernel-path"
                  type="text"
                  value={f.kernelPath}
                  onChange={(e) => set("kernelPath", e.target.value)}
                />
              </div>
              <div className="field-row">
                <div className="field">
                  <label htmlFor="f-initrd-path">
                    Initrd path <span className="muted">(optional)</span>
                  </label>
                  <input
                    id="f-initrd-path"
                    type="text"
                    value={f.initrdPath}
                    onChange={(e) => set("initrdPath", e.target.value)}
                  />
                </div>
                <div className="field">
                  <label htmlFor="f-boot-args">
                    Boot args <span className="muted">(optional, overrides default)</span>
                  </label>
                  <input
                    id="f-boot-args"
                    type="text"
                    placeholder="console=ttyS0 reboot=k panic=1 pci=off init=/init"
                    value={f.bootArgs}
                    onChange={(e) => set("bootArgs", e.target.value)}
                  />
                </div>
              </div>
            </div>
          )}

          <div className="section-label">User data</div>
          <div className="field">
            <label htmlFor="f-create-userdata-boot">
              Boot script <span className="muted">(optional)</span>
            </label>
            <textarea
              className="ta"
              id="f-create-userdata-boot"
              rows={3}
              placeholder={"#!/bin/sh\necho hello"}
              value={f.userDataBoot}
              onChange={(e) => set("userDataBoot", e.target.value)}
            />
          </div>
          <div className="field">
            <label htmlFor="f-create-userdata-system">
              System <span className="muted">(JSON, optional)</span>
            </label>
            <textarea
              className="ta"
              id="f-create-userdata-system"
              rows={2}
              placeholder='{"key": "value"}'
              value={f.userDataSystemText}
              onChange={(e) => set("userDataSystemText", e.target.value)}
            />
          </div>
        </div>
      )}
    </Modal>
  );
}
