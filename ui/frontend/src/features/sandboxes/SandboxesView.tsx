import { useMemo, useState } from "react";
import type { SandboxTrait } from "../../gen/pkg/api/runtime/v1/api_pb";
import { StatusChip } from "../../components/StatusChip";
import { SandboxDetailPanel } from "./SandboxDetailPanel";
import { CheckpointModal } from "../../modals/CheckpointModal";
import { RemoveModal } from "../../modals/RemoveModal";
import { UpdateUserDataModal } from "../../modals/UpdateUserDataModal";

export function SandboxesView({
  sandboxes,
  loading,
  refresh,
  onAttachNetwork,
}: {
  sandboxes: SandboxTrait[];
  loading: boolean;
  refresh: () => void;
  onAttachNetwork: (uid: string) => void;
}) {
  const [filter, setFilter] = useState("");
  const [selectedUid, setSelectedUid] = useState<string | null>(null);
  const [checkpointUid, setCheckpointUid] = useState<string | null>(null);
  const [removeUid, setRemoveUid] = useState<string | null>(null);
  const [userDataUid, setUserDataUid] = useState<string | null>(null);

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return sandboxes;
    return sandboxes.filter((s) => {
      const uid = s.config?.metadata?.uid ?? "";
      const image = s.config?.bootSource?.case === "rootfs" ? (s.config.bootSource.value.spec?.image ?? "") : "";
      return uid.toLowerCase().includes(q) || image.toLowerCase().includes(q);
    });
  }, [sandboxes, filter]);

  const selected = sandboxes.find((s) => s.config?.metadata?.uid === selectedUid) ?? null;
  const userDataTarget = sandboxes.find((s) => s.config?.metadata?.uid === userDataUid) ?? null;

  return (
    <>
      <section className="view active">
        <div className="view-head">
          <div>
            <div className="view-title">Sandboxes</div>
            <div className="view-sub">
              {loading
                ? "Loading…"
                : filter
                  ? `${visible.length} of ${sandboxes.length} sandboxes match "${filter}"`
                  : `${sandboxes.length} sandbox${sandboxes.length === 1 ? "" : "es"} across this node`}
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
                placeholder="Filter by uid or image"
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
                <th>Sandbox</th>
                <th>Status</th>
                <th>vCPU</th>
                <th>Memory</th>
                <th>Storage</th>
                <th>Address</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {visible.map((s) => {
                const uid = s.config?.metadata?.uid ?? "";
                const image =
                  s.config?.bootSource?.case === "rootfs"
                    ? s.config.bootSource.value.spec?.image
                    : `from checkpoint ${s.config?.bootSource?.value}`;
                return (
                  <tr key={uid} className={uid === selectedUid ? "row-selected" : ""} onClick={() => setSelectedUid(uid)}>
                    <td>
                      <div className="uid-cell">
                        <span className="uid">{uid}</span>
                        <span className="image">{image}</span>
                      </div>
                    </td>
                    <td>
                      <StatusChip state={s.state} />
                    </td>
                    <td className="num">{s.config?.resources?.cpu ?? "—"}</td>
                    <td className="num">
                      {s.config?.resources?.memoryBytes !== undefined
                        ? `${(Number(s.config.resources.memoryBytes) / (1024 * 1024)).toFixed(0)} MiB`
                        : "—"}
                    </td>
                    <td className="num">
                      {s.config?.resources?.storageBytes !== undefined
                        ? `${(Number(s.config.resources.storageBytes) / (1024 * 1024)).toFixed(0)} MiB`
                        : "—"}
                    </td>
                    <td className="num">{s.network?.ipAddress || "—"}</td>
                    <td></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>

      <SandboxDetailPanel
        sandbox={selected}
        onClose={() => setSelectedUid(null)}
        onChanged={refresh}
        onCheckpoint={setCheckpointUid}
        onRemove={setRemoveUid}
        onEditUserData={setUserDataUid}
        onAttachNetwork={onAttachNetwork}
      />

      <CheckpointModal
        open={checkpointUid !== null}
        onClose={() => setCheckpointUid(null)}
        onCheckpointed={refresh}
        sourceUid={checkpointUid}
      />
      <RemoveModal
        open={removeUid !== null}
        onClose={() => setRemoveUid(null)}
        onRemoved={() => {
          refresh();
          if (removeUid === selectedUid) setSelectedUid(null);
        }}
        targetUid={removeUid}
      />
      <UpdateUserDataModal
        open={userDataUid !== null}
        onClose={() => setUserDataUid(null)}
        onUpdated={refresh}
        targetUid={userDataUid}
        current={userDataTarget?.config?.userData}
      />
    </>
  );
}
