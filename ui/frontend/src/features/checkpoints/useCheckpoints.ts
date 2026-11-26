import { useCallback, useEffect, useState } from "react";
import { runtimeClient } from "../../client/transport";
import type { Checkpoint } from "../../gen/pkg/api/runtime/v1/api_pb";
import { toast } from "../../components/Toast";

export function useCheckpoints() {
  const [checkpoints, setCheckpoints] = useState<Checkpoint[]>([]);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const resp = await runtimeClient.listCheckpoint({});
      setCheckpoints(resp.checkpoint);
    } catch (err) {
      toast(`Failed to list checkpoints: ${(err as Error).message}`, true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { checkpoints, loading, refresh };
}
