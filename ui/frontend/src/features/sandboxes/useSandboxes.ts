import { useCallback, useEffect, useState } from "react";
import { runtimeClient } from "../../client/transport";
import type { SandboxTrait } from "../../gen/pkg/api/runtime/v1/api_pb";
import { toast } from "../../components/Toast";

export function useSandboxes() {
  const [sandboxes, setSandboxes] = useState<SandboxTrait[]>([]);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const resp = await runtimeClient.listSandbox({});
      setSandboxes(resp.sandbox);
    } catch (err) {
      toast(`Failed to list sandboxes: ${(err as Error).message}`, true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { sandboxes, loading, refresh };
}
