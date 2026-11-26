import { useEffect, useState } from "react";
import { rootFSClient } from "../client/transport";
import { toast } from "../components/Toast";
import { CloseIcon } from "../components/Modal";
import type { RootFS } from "../gen/pkg/api/runtime/v1/api_pb";

export function RootFSStatusModal({
  open,
  onClose,
  image,
}: {
  open: boolean;
  onClose: () => void;
  image: string | null;
}) {
  const [rootfs, setRootfs] = useState<RootFS | undefined>();
  const [info, setInfo] = useState<Record<string, string>>({});
  const [verbose, setVerbose] = useState(false);

  useEffect(() => {
    if (!open || !image) return;
    rootFSClient
      .rootFSStatus({ spec: { image }, verbose })
      .then((resp) => {
        setRootfs(resp.rootfs);
        setInfo(resp.info);
      })
      .catch((err: Error) => toast(`Rootfs status failed: ${err.message}`, true));
  }, [open, image, verbose]);

  return (
    <div
      className={`modal-scrim${open ? " open" : ""}`}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal">
        <div className="modal-head">
          <h2>Rootfs status</h2>
          <button type="button" className="panel-close" aria-label="Close" onClick={onClose}>
            <CloseIcon />
          </button>
        </div>
        <div className="modal-body">
          <table className="kv-table">
            <tbody>
              <tr>
                <td>Image</td>
                <td>{rootfs?.spec?.image ?? image}</td>
              </tr>
              <tr>
                <td>ID</td>
                <td className="mono">{rootfs?.id ?? "—"}</td>
              </tr>
              <tr>
                <td>Repo tags</td>
                <td className="mono">{rootfs?.repoTags.join(", ") || "—"}</td>
              </tr>
              <tr>
                <td>Repo digests</td>
                <td className="mono">{rootfs?.repoDigests.join(", ") || "—"}</td>
              </tr>
              <tr>
                <td>Size</td>
                <td>{rootfs ? `${rootfs.size} bytes` : "—"}</td>
              </tr>
            </tbody>
          </table>
          <label
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              fontWeight: 500,
              fontSize: 13,
              marginTop: 14,
            }}
          >
            <input
              type="checkbox"
              style={{ width: "auto" }}
              checked={verbose}
              onChange={(e) => setVerbose(e.target.checked)}
            />
            Verbose
          </label>
          {verbose && (
            <div style={{ marginTop: 10 }}>
              <div className="section-label">Info</div>
              <table className="kv-table">
                <tbody>
                  {Object.entries(info).length === 0 ? (
                    <tr>
                      <td colSpan={2}>No extra info reported.</td>
                    </tr>
                  ) : (
                    Object.entries(info).map(([k, v]) => (
                      <tr key={k}>
                        <td>{k}</td>
                        <td>{v}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          )}
        </div>
        <div className="modal-foot">
          <button type="button" className="btn" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
