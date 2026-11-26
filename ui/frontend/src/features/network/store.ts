// NetworkService has no "list attachments" RPC (see api.proto -- only
// AttachNetwork/DetachNetwork exist), so unlike sandboxes/rootfs/
// checkpoints there's no server-backed list to refetch. This is a
// session-local record of what AttachNetwork/DetachNetwork have
// actually returned/removed in this browser tab -- real handles, just
// not persisted across a reload. The Network view and each sandbox's
// own detail-panel Network tab both read from it.
import { useSyncExternalStore } from "react";

export type Attachment = {
  sandboxId: string;
  netnsPath: string;
  tapDevice: string;
  ipAddress: string;
  gatewayIp: string;
};

let attachments: Record<string, Attachment> = {};
// useSyncExternalStore requires getSnapshot to return a referentially
// stable value when nothing changed -- Object.values(attachments)
// would allocate a new array every render and loop forever (React
// error #185). list is recomputed only inside emit(), once per actual
// mutation, so repeated getSnapshot calls between mutations return the
// same reference.
let list: Attachment[] = [];
const listeners = new Set<() => void>();

function emit() {
  list = Object.values(attachments);
  for (const l of listeners) l();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function setAttachment(a: Attachment) {
  attachments = { ...attachments, [a.sandboxId]: a };
  emit();
}

export function removeAttachment(sandboxId: string) {
  if (!(sandboxId in attachments)) return;
  const rest = { ...attachments };
  delete rest[sandboxId];
  attachments = rest;
  emit();
}

export function useNetworkAttachments(): Attachment[] {
  return useSyncExternalStore(subscribe, () => list);
}

export function useNetworkAttachment(sandboxId: string): Attachment | undefined {
  return useSyncExternalStore(subscribe, () => attachments[sandboxId]);
}
