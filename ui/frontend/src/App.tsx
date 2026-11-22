import { useState } from "react";
import { useSandboxes } from "./features/sandboxes/useSandboxes";
import { useRootfs } from "./features/rootfs/useRootfs";
import { useCheckpoints } from "./features/checkpoints/useCheckpoints";
import { useNetworkAttachments } from "./features/network/store";
import { SandboxesView } from "./features/sandboxes/SandboxesView";
import { RootfsView } from "./features/rootfs/RootfsView";
import { CheckpointsView } from "./features/checkpoints/CheckpointsView";
import { NetworkView } from "./features/network/NetworkView";
import { CreateSandboxModal } from "./modals/CreateSandboxModal";
import { PullRootfsModal } from "./modals/PullRootfsModal";
import { AttachNetworkModal } from "./modals/AttachNetworkModal";
import { RootFSStatusModal } from "./modals/RootFSStatusModal";
import { Toast } from "./components/Toast";

type View = "sandboxes" | "rootfs" | "checkpoints" | "network";

const NAV: { view: View; label: string; icon: React.ReactNode }[] = [
  {
    view: "sandboxes",
    label: "Sandboxes",
    icon: (
      <svg viewBox="0 0 24 24" fill="none">
        <rect x="3" y="4" width="18" height="16" rx="2" stroke="currentColor" strokeWidth="1.6" />
        <path d="M3 9h18" stroke="currentColor" strokeWidth="1.6" />
      </svg>
    ),
  },
  {
    view: "rootfs",
    label: "Rootfs images",
    icon: (
      <svg viewBox="0 0 24 24" fill="none">
        <ellipse cx="12" cy="6" rx="8" ry="3" stroke="currentColor" strokeWidth="1.6" />
        <path d="M4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6" stroke="currentColor" strokeWidth="1.6" />
        <path d="M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3" stroke="currentColor" strokeWidth="1.6" />
      </svg>
    ),
  },
  {
    view: "checkpoints",
    label: "Checkpoints",
    icon: (
      <svg viewBox="0 0 24 24" fill="none">
        <rect x="3" y="7" width="18" height="13" rx="2" stroke="currentColor" strokeWidth="1.6" />
        <path d="M8 7l1.5-2.5h5L16 7" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
        <circle cx="12" cy="13.5" r="3.5" stroke="currentColor" strokeWidth="1.6" />
      </svg>
    ),
  },
  {
    view: "network",
    label: "Network",
    icon: (
      <svg viewBox="0 0 24 24" fill="none">
        <circle cx="12" cy="5" r="2.2" stroke="currentColor" strokeWidth="1.6" />
        <circle cx="5" cy="19" r="2.2" stroke="currentColor" strokeWidth="1.6" />
        <circle cx="19" cy="19" r="2.2" stroke="currentColor" strokeWidth="1.6" />
        <path d="M12 7.2V13M12 13L6.4 17.3M12 13l5.6 4.3" stroke="currentColor" strokeWidth="1.6" />
      </svg>
    ),
  },
];

export default function App() {
  const [view, setView] = useState<View>("sandboxes");

  const sandboxes = useSandboxes();
  const rootfs = useRootfs();
  const checkpoints = useCheckpoints();
  const attachments = useNetworkAttachments();

  const [createOpen, setCreateOpen] = useState(false);
  const [createFromCheckpoint, setCreateFromCheckpoint] = useState<string | null>(null);
  const [pullOpen, setPullOpen] = useState(false);
  const [attachOpen, setAttachOpen] = useState(false);
  const [attachPrefillUid, setAttachPrefillUid] = useState<string | undefined>(undefined);
  const [statusImage, setStatusImage] = useState<string | null>(null);

  const counts: Record<View, number> = {
    sandboxes: sandboxes.sandboxes.length,
    rootfs: rootfs.images.length,
    checkpoints: checkpoints.checkpoints.length,
    network: attachments.length,
  };

  return (
    <div className="shell">
      <div className="topbar">
        <div className="brand">
          <span className="brand-mark">
            <svg viewBox="0 0 24 24" fill="none">
              <path
                d="M4 7L12 3L20 7V17L12 21L4 17V7Z"
                stroke="white"
                strokeWidth="1.6"
                strokeLinejoin="round"
              />
              <path d="M4 7L12 11L20 7" stroke="white" strokeWidth="1.6" strokeLinejoin="round" />
              <path d="M12 11V21" stroke="white" strokeWidth="1.6" />
            </svg>
          </span>
          Backbone Console
          <span className="brand-sub">sri runtime</span>
        </div>
        <div className="topbar-right">
          <button className="btn" onClick={() => setPullOpen(true)}>
            <svg viewBox="0 0 16 16" fill="none" width="14" height="14">
              <path d="M8 1v14M1 8h14" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
            </svg>
            Pull rootfs
          </button>
          <button
            className="btn btn-primary"
            onClick={() => {
              setCreateFromCheckpoint(null);
              setCreateOpen(true);
            }}
          >
            <svg viewBox="0 0 16 16" fill="none" width="14" height="14">
              <path d="M8 1v14M1 8h14" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
            </svg>
            Create sandbox
          </button>
        </div>
      </div>

      <nav className="nav">
        {NAV.map((n) => (
          <button
            key={n.view}
            className={`nav-item${view === n.view ? " active" : ""}`}
            onClick={() => setView(n.view)}
          >
            {n.icon}
            {n.label}
            <span className="nav-count">{counts[n.view]}</span>
          </button>
        ))}
      </nav>

      <main className="main">
        {view === "sandboxes" && (
          <SandboxesView
            sandboxes={sandboxes.sandboxes}
            loading={sandboxes.loading}
            refresh={sandboxes.refresh}
            onAttachNetwork={(uid) => {
              setAttachPrefillUid(uid);
              setAttachOpen(true);
            }}
          />
        )}
        {view === "rootfs" && (
          <RootfsView
            images={rootfs.images}
            loading={rootfs.loading}
            refresh={rootfs.refresh}
            onStatus={setStatusImage}
          />
        )}
        {view === "checkpoints" && (
          <CheckpointsView
            checkpoints={checkpoints.checkpoints}
            loading={checkpoints.loading}
            onRestore={(ref) => {
              setCreateFromCheckpoint(ref);
              setCreateOpen(true);
            }}
          />
        )}
        {view === "network" && (
          <NetworkView
            onAttach={() => {
              setAttachPrefillUid(undefined);
              setAttachOpen(true);
            }}
          />
        )}
      </main>

      <CreateSandboxModal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={sandboxes.refresh}
        images={rootfs.images}
        checkpoints={checkpoints.checkpoints}
        initialBootSource={createFromCheckpoint ? { source: "checkpoint", ref: createFromCheckpoint } : undefined}
      />
      <PullRootfsModal open={pullOpen} onClose={() => setPullOpen(false)} onPulled={rootfs.refresh} />
      <AttachNetworkModal
        open={attachOpen}
        onClose={() => setAttachOpen(false)}
        prefillUid={attachPrefillUid}
        knownUids={sandboxes.sandboxes.map((s) => s.config?.metadata?.uid ?? "").filter(Boolean)}
      />
      <RootFSStatusModal open={statusImage !== null} onClose={() => setStatusImage(null)} image={statusImage} />

      <Toast />
    </div>
  );
}
