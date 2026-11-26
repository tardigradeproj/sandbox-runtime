import { SandboxStateValue } from "../gen/pkg/api/runtime/v1/api_pb";

const CHIP: Record<SandboxStateValue, { label: string; cls: string }> = {
  [SandboxStateValue.STATE_UNSPECIFIED]: { label: "Unknown", cls: "chip-stopped" },
  [SandboxStateValue.STATE_RUNNING]: { label: "Running", cls: "chip-running" },
  [SandboxStateValue.STATE_STOPPED]: { label: "Stopped", cls: "chip-stopped" },
  [SandboxStateValue.STATE_PENDING]: { label: "Pending", cls: "chip-transition" },
};

export function StatusChip({ state }: { state: SandboxStateValue }) {
  const { label, cls } = CHIP[state];
  return (
    <span className={`chip ${cls}`}>
      <span className="chip-dot" />
      {label}
    </span>
  );
}
