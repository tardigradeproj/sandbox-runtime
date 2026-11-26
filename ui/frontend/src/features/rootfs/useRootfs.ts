import { useCallback, useEffect, useState } from "react";
import { rootFSClient } from "../../client/transport";
import type { RootFS } from "../../gen/pkg/api/runtime/v1/api_pb";
import { toast } from "../../components/Toast";

export function useRootfs() {
  const [images, setImages] = useState<RootFS[]>([]);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const resp = await rootFSClient.listRootFS({});
      setImages(resp.rootfs);
    } catch (err) {
      toast(`Failed to list rootfs images: ${(err as Error).message}`, true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { images, loading, refresh };
}
