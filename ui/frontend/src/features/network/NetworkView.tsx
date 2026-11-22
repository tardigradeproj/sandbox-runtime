import { networkClient } from "../../client/transport";
import { toast } from "../../components/Toast";
import { removeAttachment, useNetworkAttachments } from "./store";

export function NetworkView({ onAttach }: { onAttach: () => void }) {
  const attachments = useNetworkAttachments();

  async function detach(sandboxId: string) {
    try {
      await networkClient.detachNetwork({ sandboxId });
      removeAttachment(sandboxId);
      toast(`Detached network for ${sandboxId}`);
    } catch (err) {
      toast(`Detach network failed: ${(err as Error).message}`, true);
    }
  }

  return (
    <section className="view active">
      <div className="view-head">
        <div>
          <div className="view-title">Network</div>
          <div className="view-sub">
            {attachments.length} attached namespace{attachments.length === 1 ? "" : "s"} on this node's
            bridge (sri0), attached in this session
          </div>
        </div>
        <button className="btn btn-primary" onClick={onAttach}>
          <svg viewBox="0 0 16 16" fill="none" width="14" height="14">
            <path d="M8 1v14M1 8h14" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
          </svg>
          Attach network
        </button>
      </div>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Sandbox</th>
              <th>Namespace</th>
              <th>Tap device</th>
              <th>Address</th>
              <th>Gateway</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {attachments.length === 0 ? (
              <tr>
                <td colSpan={6}>
                  <div className="empty-state">No network attachments in this session yet.</div>
                </td>
              </tr>
            ) : (
              attachments.map((a) => (
                <tr key={a.sandboxId}>
                  <td style={{ fontWeight: 500, color: "var(--text)" }} className="num">
                    {a.sandboxId}
                  </td>
                  <td className="num">{a.netnsPath}</td>
                  <td className="num">{a.tapDevice}</td>
                  <td className="num">{a.ipAddress}</td>
                  <td className="num">{a.gatewayIp}</td>
                  <td>
                    <div className="row-actions">
                      <button className="icon-btn" title="Detach network" onClick={() => detach(a.sandboxId)}>
                        <svg viewBox="0 0 16 16" fill="none">
                          <path
                            d="M4 4l8 8M12 4l-8 8"
                            stroke="currentColor"
                            strokeWidth="1.5"
                            strokeLinecap="round"
                          />
                        </svg>
                      </button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}
