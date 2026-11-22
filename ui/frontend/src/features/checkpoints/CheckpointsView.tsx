import { useMemo, useState } from "react";
import type { Checkpoint } from "../../gen/pkg/api/runtime/v1/api_pb";
import type { Timestamp } from "@bufbuild/protobuf/wkt";

function formatTimestamp(ts: Timestamp | undefined): string {
  if (!ts) return "—";
  return new Date(Number(ts.seconds) * 1000).toISOString();
}

export function CheckpointsView({
  checkpoints,
  loading,
  onRestore,
}: {
  checkpoints: Checkpoint[];
  loading: boolean;
  onRestore: (ref: string) => void;
}) {
  const [filter, setFilter] = useState("");

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return checkpoints;
    return checkpoints.filter(
      (c) =>
        c.checkpointRef.toLowerCase().includes(q) ||
        (c.config?.metadata?.uid ?? "").toLowerCase().includes(q),
    );
  }, [checkpoints, filter]);

  return (
    <section className="view active">
      <div className="view-head">
        <div>
          <div className="view-title">Checkpoints</div>
          <div className="view-sub">
            {loading
              ? "Loading…"
              : filter
                ? `${visible.length} of ${checkpoints.length} checkpoints match "${filter}"`
                : `${checkpoints.length} checkpoint${checkpoints.length === 1 ? "" : "s"} saved`}
          </div>
        </div>
        <div className="toolbar">
          <div className="search">
            <svg viewBox="0 0 24 24" fill="none">
              <circle cx="11" cy="11" r="7" stroke="currentColor" strokeWidth="1.8" />
              <path d="M21 21l-4.3-4.3" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
            </svg>
            <input
              type="text"
              placeholder="Filter by ref or sandbox"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
          </div>
        </div>
      </div>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Checkpoint</th>
              <th>Source sandbox</th>
              <th>User</th>
              <th>Address</th>
              <th>Created</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {visible.length === 0 ? (
              <tr>
                <td colSpan={6}>
                  <div className="empty-state">
                    No checkpoints yet — open a running sandbox and use its <b>Checkpoint</b> action.
                  </div>
                </td>
              </tr>
            ) : (
              visible.map((c) => (
                <tr key={c.checkpointRef}>
                  <td>
                    <div className="uid-cell">
                      <span className="uid">{c.checkpointRef}</span>
                      <span className="subtitle">{c.name || "—"}</span>
                    </div>
                  </td>
                  <td>
                    <div className="uid-cell">
                      <span className="uid">{c.config?.metadata?.uid}</span>
                      <span className="image">
                        {c.config?.bootSource?.case === "rootfs" ? c.config.bootSource.value.spec?.image : "—"}
                      </span>
                    </div>
                  </td>
                  <td className="num">
                    {c.user ? `${c.user.uid}:${c.user.gid}` : "—"}
                  </td>
                  <td className="num">{c.network?.ipAddress || "—"}</td>
                  <td className="num">{formatTimestamp(c.createdAt)}</td>
                  <td>
                    <div className="row-actions">
                      <button
                        className="icon-btn"
                        title="Create sandbox from this checkpoint"
                        onClick={() => onRestore(c.checkpointRef)}
                      >
                        <svg viewBox="0 0 16 16" fill="none">
                          <path d="M3 3l10 5-10 5" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />
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
