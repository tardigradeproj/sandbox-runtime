import { useMemo, useState } from "react";
import type { RootFS } from "../../gen/pkg/api/runtime/v1/api_pb";
import { rootFSClient } from "../../client/transport";
import { toast } from "../../components/Toast";

export function RootfsView({
  images,
  loading,
  refresh,
  onStatus,
}: {
  images: RootFS[];
  loading: boolean;
  refresh: () => void;
  onStatus: (image: string) => void;
}) {
  const [filter, setFilter] = useState("");

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return images;
    return images.filter(
      (r) =>
        (r.spec?.image ?? "").toLowerCase().includes(q) ||
        Object.entries(r.spec?.annotations ?? {}).some(([k, v]) => `${k}=${v}`.toLowerCase().includes(q)),
    );
  }, [images, filter]);

  async function pullAgain(image: string, annotations: Record<string, string>) {
    try {
      await rootFSClient.pullRootFS({ spec: { image, annotations } });
      toast(`Pulling ${image}…`);
      refresh();
    } catch (err) {
      toast(`Pull rootfs failed: ${(err as Error).message}`, true);
    }
  }

  async function remove(image: string) {
    try {
      await rootFSClient.removeRootFS({ spec: { image } });
      toast(`Removed ${image}`);
      refresh();
    } catch (err) {
      toast(`Remove rootfs failed: ${(err as Error).message}`, true);
    }
  }

  return (
    <section className="view active">
      <div className="view-head">
        <div>
          <div className="view-title">Rootfs images</div>
          <div className="view-sub">
            {loading
              ? "Loading…"
              : filter
                ? `${visible.length} of ${images.length} images match "${filter}"`
                : `${images.length} image${images.length === 1 ? "" : "s"} cached locally`}
          </div>
        </div>
        <div className="toolbar">
          <div className="search">
            <SearchIcon />
            <input
              type="text"
              placeholder="Filter by image or annotation"
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
              <th>Image</th>
              <th>Digest</th>
              <th>Size</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {visible.map((r) => {
              const image = r.spec?.image ?? r.repoTags[0] ?? "";
              return (
                <tr key={image}>
                  <td style={{ fontWeight: 500, color: "var(--text)" }} className="num">
                    {image}
                  </td>
                  <td className="num">{r.repoDigests[0] ?? "—"}</td>
                  <td className="num">{`${(Number(r.size) / 1_000_000).toFixed(1)} MB`}</td>
                  <td>
                    <div className="row-actions">
                      <button className="icon-btn" title="Status" onClick={() => onStatus(image)}>
                        <svg viewBox="0 0 16 16" fill="none">
                          <path
                            d="M1 8s2.7-5 7-5 7 5 7 5-2.7 5-7 5-7-5-7-5z"
                            stroke="currentColor"
                            strokeWidth="1.4"
                          />
                          <circle cx="8" cy="8" r="2" stroke="currentColor" strokeWidth="1.4" />
                        </svg>
                      </button>
                      <button
                        className="icon-btn"
                        title="Pull again"
                        onClick={() => pullAgain(image, r.spec?.annotations ?? {})}
                      >
                        <svg viewBox="0 0 16 16" fill="none">
                          <path
                            d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2v3.3h-3.3"
                            stroke="currentColor"
                            strokeWidth="1.4"
                            strokeLinecap="round"
                            strokeLinejoin="round"
                          />
                        </svg>
                      </button>
                      <button className="icon-btn" title="Remove" onClick={() => remove(image)}>
                        <svg viewBox="0 0 16 16" fill="none">
                          <path
                            d="M3 4h10M6.5 4V2.5h3V4M4.5 4l.6 9h5.8l.6-9"
                            stroke="currentColor"
                            strokeWidth="1.4"
                            strokeLinecap="round"
                            strokeLinejoin="round"
                          />
                        </svg>
                      </button>
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function SearchIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none">
      <circle cx="11" cy="11" r="7" stroke="currentColor" strokeWidth="1.8" />
      <path d="M21 21l-4.3-4.3" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
    </svg>
  );
}
