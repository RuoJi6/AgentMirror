// A view of observed traffic only: raw records and evidence remain untouched.
// Query values, retries and response variants stay available in each group's records.
const staticAsset =
  /\.(?:css|js|mjs|map|png|jpe?g|gif|svg|ico|webp|woff2?|ttf)$/i;

export function summarizeAccessFlow(nodes = [], edges = [], options = {}) {
  const byID = new Map(nodes.map((node) => [node.id, node]));
  const children = new Map(),
    childIDs = new Set();
  for (const edge of edges) {
    if (edge.relation === "observed_next") continue;
    if (!byID.has(edge.source) || !byID.has(edge.target)) continue;
    if (!children.has(edge.source)) children.set(edge.source, []);
    children.get(edge.source).push(byID.get(edge.target));
    childIDs.add(edge.target);
  }
  const buckets = new Map();
  for (const node of nodes) {
    if (childIDs.has(node.id)) continue;
    const e = node.evidence || {},
      related = children.get(node.id) || [];
    const path = String(e.path || "").split(/[?#]/, 1)[0];
    const method = e.method || "";
    const has = (kind) =>
      node.kind === kind || related.some((n) => n.kind === kind);
    const important =
      has("delivery") || has("callback") || has("download") || has("rule");
    const noise =
      !important && Number(e.status) === 404
        ? "not_found"
        : !important &&
            ["GET", "HEAD"].includes(method) &&
            Number(e.status) < 400 &&
            staticAsset.test(path)
          ? "static"
          : "";
    // Separate ports, hosts, deployments and legacy event kinds. A shared IP is
    // only a source filter, never proof that these records came from one agent.
    const origin = [e.deployment_id, e.listener_id, e.listener?.port, e.host];
    const key = JSON.stringify([
      ...origin,
      ...(options.scopeKey ? [options.scopeKey(node)] : []),
      noise || node.kind,
      ...(noise ? [] : [method, path || node.label]),
    ]);
    let bucket = buckets.get(key);
    if (!bucket) {
      bucket = {
        id: `summary:${key}`,
        group: node.group,
        kind: node.kind,
        label:
          noise === "not_found"
            ? "未找到的路径 · 404"
            : noise === "static"
              ? "静态资源"
              : [method, path].filter(Boolean).join(" ") || node.label,
        status: "observed",
        summary: {
          records: [],
          statuses: {},
          delivery: 0,
          download: 0,
          accepted: 0,
          rejected: 0,
          rules: 0,
          port: e.listener?.port,
          noise,
          first: e.at,
          last: e.at,
        },
      };
      buckets.set(key, bucket);
    }
    const s = bucket.summary;
    s.records.push({ ...node, related });
    s.last = e.at;
    if (e.status != null)
      s.statuses[e.status] = (s.statuses[e.status] || 0) + 1;
    for (const item of [node, ...related]) {
      if (item.kind === "delivery") s.delivery++;
      if (item.kind === "download") s.download++;
      if (item.kind === "rule") s.rules++;
      if (item.kind === "callback")
        s[item.status === "fail" ? "rejected" : "accepted"]++;
    }
    bucket.kind = s.delivery
      ? "delivery"
      : s.accepted || s.rejected
        ? "callback"
        : s.download
          ? "download"
          : node.kind;
    const failures = s.records.filter((r) => r.status === "fail").length;
    bucket.status =
      failures === s.records.length ? "fail" : failures ? "mixed" : "observed";
  }
  const grouped = [...buckets.values()];
  grouped.forEach((node, i) => {
    node.summary.order = i + 1;
  });
  return {
    nodes: grouped,
    // Order of first occurrence within this page, not a claim of causality or
    // consecutive requests. The complete graph retains every temporal edge.
    edges: grouped.slice(1).map((node, i) => ({
      source: grouped[i].id,
      target: node.id,
      relation: "first_seen",
    })),
    recordCount: grouped.reduce(
      (sum, node) => sum + node.summary.records.length,
      0,
    ),
  };
}

export const COMPACT_NODE_HEIGHT = 156;
export function layoutAccessSummary(nodes = []) {
  const columns = Math.min(4, Math.max(1, nodes.length));
  const positions = new Map(
    nodes.map((node, i) => {
      const row = Math.floor(i / columns),
        column = i % columns;
      return [
        node.id,
        {
          x: 40 + (row % 2 ? columns - column - 1 : column) * 310,
          y: 110 + row * 220,
        },
      ];
    }),
  );
  return {
    positions,
    width: columns * 310 + 40,
    height: Math.ceil(nodes.length / columns) * 220 + 110,
  };
}
