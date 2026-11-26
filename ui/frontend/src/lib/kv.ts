// parseKVLines turns "one key=value per line" textarea content into
// the map<string, string> shape labels/label_selector/rootfs
// annotations are wire-represented as. bad holds lines missing "="
// so the caller can reject the submit instead of silently dropping them.
export function parseKVLines(text: string): {
  map: Record<string, string>;
  bad: string[];
} {
  const lines = text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
  const bad = lines.filter((l) => !l.includes("="));
  const map: Record<string, string> = {};
  for (const line of lines) {
    const i = line.indexOf("=");
    if (i === -1) continue;
    map[line.slice(0, i)] = line.slice(i + 1);
  }
  return { map, bad };
}

// formatKVLines is parseKVLines's inverse, for pre-filling a textarea
// from an existing map<string, string>.
export function formatKVLines(map: Record<string, string> | undefined): string {
  if (!map) return "";
  return Object.entries(map)
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");
}
