import { summarizeAccessFlow } from "./access-flow-summary.js";

// ATT&CK-inspired reading order. These are observable HTTP categories, not
// asserted techniques or evidence of successful compromise.
export const attackPhases = [
  { label: "访问来源 / 站点", color: "#64748b" },
  { label: "入口与侦察", color: "#2563eb" },
  { label: "认证尝试", color: "#d97706" },
  { label: "接口与文件探索", color: "#0891b2" },
  { label: "内容获取 / 提示词交付", color: "#8b5cf6" },
  { label: "回传结果", color: "#059669" },
];
const pathOf = (record) =>
  String(record.evidence?.path || "").split(/[?#]/, 1)[0];
function phaseOf(record) {
  const e = record.evidence || {},
    path = pathOf(record);
  const kinds = [record.kind, ...record.related.map((n) => n.kind)];
  if (kinds.includes("callback")) return 5;
  if (kinds.includes("delivery") || kinds.includes("download")) return 4;
  if (
    /(?:^|\/)(?:login|signin|auth|session|mfa|otp|oauth|token)(?:[/.\-_]|$)/i.test(
      path,
    )
  )
    return 2;
  if (
    /\/api\/|(?:config|keys|secrets|passwd|\.env|backup|download)/i.test(path)
  )
    return 3;
  if (e.status >= 400) return 3;
  return 1;
}
const hasDelivery = (record) =>
  record.kind === "delivery" ||
  record.related.some((n) => n.kind === "delivery");
const isCallback = (record) =>
  record.kind === "callback" ||
  record.related.some((n) => n.kind === "callback");
const originOf = (record) => {
  const e = record.evidence || {};
  return JSON.stringify([
    e.deployment_id,
    e.listener_id,
    e.listener?.port,
    e.host,
  ]);
};
function requestURL(record) {
  const e = record.evidence || {};
  try {
    return new URL(
      e.path || "/",
      e.host ? `http://${e.host}` : e.listener?.public_url,
    ).href;
  } catch {
    return "";
  }
}
const isBackground = (r) =>
  /^AgentMirror-Deployment-Healthcheck\//.test(r.evidence?.ua || "") ||
  (r.related.length === 0 &&
    r.evidence?.status < 400 &&
    /\.(css|js|mjs|map|png|jpe?g|gif|svg|ico|webp|woff2?|ttf)$/i.test(
      pathOf(r),
    ));

export function buildAttackFlow(
  rawNodes = [],
  rawEdges = [],
  { includeBackground = false } = {},
) {
  const order = new Map(rawNodes.map((n, i) => [n.id, i]));
  const all = summarizeAccessFlow(rawNodes, rawEdges)
    .nodes.flatMap((n) => n.summary.records)
    .sort((a, b) => order.get(a.id) - order.get(b.id));
  const records = all.filter((r) => includeBackground || !isBackground(r));
  const ids = new Set(
    records.flatMap((r) => [r.id, ...r.related.map((c) => c.id)]),
  );
  const trailByID = new Map(),
    trails = new Map(),
    lastByUA = new Map();
  for (const r of records) {
    const e = r.evidence || {},
      identity = JSON.stringify([e.ip, e.ua]);
    const at = Date.parse(e.at),
      previous = lastByUA.get(identity);
    const newTrail =
      !previous ||
      !Number.isFinite(at) ||
      !Number.isFinite(previous.at) ||
      at - previous.at > 30 * 60 * 1000;
    const trail = newTrail ? `${identity}:${r.id}` : previous.trail;
    if (!trails.has(trail))
      trails.set(trail, {
        id: trail,
        ip: e.ip,
        ua: e.ua || "未提供 User-Agent",
        records: [],
      });
    trails.get(trail).records.push(r);
    trailByID.set(r.id, trail);
    lastByUA.set(identity, { trail, at });
  }
  const summary = summarizeAccessFlow(
    rawNodes.filter((n) => ids.has(n.id)),
    rawEdges,
    {
      scopeKey: (r) => trailByID.get(r.id),
    },
  );
  const byRecord = new Map();
  for (const node of summary.nodes) {
    const first = node.summary.records[0];
    // A later delivery upgrades the endpoint's display stage, while each
    // observation below retains its own stage and exact chronological parent.
    const phase = Math.max(...node.summary.records.map(phaseOf));
    node.attack = {
      phase,
      trail: trailByID.get(first.id),
      origin: originOf(first),
    };
    for (const r of node.summary.records) byRecord.set(r.id, node);
  }
  const roots = new Map(),
    parentByRecord = new Map(),
    edgeGroups = new Map();
  const addEdge = (source, target, relation, recordID, parentID) => {
    if (source === target) return;
    const key = JSON.stringify([source, target, relation]);
    if (!edgeGroups.has(key))
      edgeGroups.set(key, { source, target, relation, observations: [] });
    edgeGroups.get(key).observations.push({
      record_id: recordID,
      parent_record_id: parentID || null,
    });
  };
  for (const trail of trails.values()) {
    const earlier = [];
    for (const r of trail.records) {
      const e = r.evidence || {},
        origin = originOf(r),
        phase = phaseOf(r),
        target = byRecord.get(r.id);
      const rootID = `origin:${trail.id}:${origin}`;
      if (!roots.has(rootID))
        roots.set(rootID, {
          id: rootID,
          kind: "source",
          group: r.group,
          status: "observed",
          label: `${e.listener?.name || "站点入口"}${e.listener?.port ? ` · :${e.listener.port}` : ""}`,
          evidence: {
            ip: e.ip,
            ua: trail.ua,
            host: e.host,
            listener: e.listener,
            source_note:
              "同 IP 与 User-Agent 的时间段关联（空闲 30 分钟分段），不能据此唯一确认同一个 AI。",
          },
          attack: { phase: 0, trail: trail.id, origin },
        });
      let parent,
        relation = "origin";
      // A callback's session links to the delivery which created its receipt
      // context. Never invent a successful callback from HTTP status alone.
      if (isCallback(r) && e.session_id) {
        parent = earlier.findLast(
          (p) => p.evidence?.session_id === e.session_id && hasDelivery(p),
        );
        if (parent) relation = "receipt_context";
      }
      if (!parent && e.referer) {
        parent = earlier.findLast((p) => requestURL(p) === e.referer);
        if (parent) relation = "referer";
      }
      if (!parent) {
        parent = earlier.findLast((p) => {
          if (
            !p.evidence?.location ||
            p.evidence?.status < 300 ||
            p.evidence?.status >= 400
          )
            return false;
          try {
            return (
              new URL(p.evidence.location, requestURL(p)).href === requestURL(r)
            );
          } catch {
            return false;
          }
        });
        if (parent) relation = "redirect";
      }
      if (!parent && e.session_id) {
        parent = earlier.findLast(
          (p) =>
            p.evidence?.session_id === e.session_id &&
            originOf(p) === origin &&
            byRecord.get(p.id)?.id !== target.id,
        );
        if (parent) relation = "same_session";
      }
      if (!parent) {
        // Inference only: attach parallel probes to a preceding lower phase,
        // instead of chaining every request or jumping across unrelated ports.
        parent = earlier.findLast(
          (p) =>
            originOf(p) === origin &&
            phaseOf(p) < phase &&
            p.evidence?.status < 400,
        );
        if (parent) relation = "inferred_phase";
      }
      const source = parent ? byRecord.get(parent.id).id : rootID;
      parentByRecord.set(r.id, {
        parent: parent?.id || null,
        rootID,
        source,
        target: target.id,
        relation,
      });
      addEdge(source, target.id, relation, r.id, parent?.id);
      earlier.push(r);
    }
  }
  const nonLeaves = new Set(
    [...parentByRecord.values()].map((p) => p.parent).filter(Boolean),
  );
  const routes = records.map((r) => {
    const chain = [],
      nodeIDs = [],
      routeEdges = [];
    let id = r.id;
    // Parents refer strictly to earlier observations, even if endpoint-level
    // aggregation forms a cycle. Route tracing therefore cannot invent loops.
    while (id) {
      chain.unshift(id);
      id = parentByRecord.get(id)?.parent;
    }
    const first = parentByRecord.get(chain[0]);
    if (first) nodeIDs.push(first.rootID);
    for (const recordID of chain) {
      const p = parentByRecord.get(recordID);
      if (!nodeIDs.includes(p.target)) nodeIDs.push(p.target);
      if (p.source !== p.target)
        routeEdges.push({
          source: p.source,
          target: p.target,
          relation: p.relation,
        });
    }
    return {
      id: r.id,
      label: `${r.evidence?.at ? new Date(r.evidence.at).toLocaleTimeString() : "历史记录"} · ${r.label} · HTTP ${r.evidence?.status ?? "—"}`,
      nodeIDs,
      edges: routeEdges,
      recordIDs: chain,
      terminal: !nonLeaves.has(r.id) || phaseOf(r) >= 4,
      inferred: routeEdges.some((e) =>
        ["inferred_phase", "same_session"].includes(e.relation),
      ),
    };
  });
  return {
    nodes: [...roots.values(), ...summary.nodes],
    edges: [...edgeGroups.values()],
    routes,
    recordCount: records.length,
    hiddenCount: all.length - records.length,
    trails: [...trails.values()],
  };
}

export function layoutAttackFlow(
  nodes = [],
  edges = [],
  { routeOnly = false } = {},
) {
  const phases = Array.from({ length: 6 }, () => []);
  for (const n of nodes) phases[n.attack?.phase || 0].push(n);
  const visiblePhases = phases.flatMap((items, i) =>
    !routeOnly || items.length ? [i] : [],
  );
  const lanes = [...new Set(nodes.map((n) => n.attack?.trail))];
  const positions = new Map();
  let top = 100;
  for (const lane of lanes) {
    const columns = phases.map((list) =>
      list.filter((n) => n.attack?.trail === lane),
    );
    const parents = new Map();
    for (const e of edges) {
      if (!parents.has(e.target)) parents.set(e.target, []);
      parents.get(e.target).push(e.source);
    }
    const height = Math.max(1, ...columns.map((c) => c.length));
    columns.forEach((column, phase) => {
      // Keep sibling branches near their preceding phase; ties retain time order.
      const score = (n) => {
        const near = (parents.get(n.id) || [])
          .map((id) => positions.get(id)?.y)
          .filter((y) => y !== undefined);
        return near.length
          ? near.reduce((a, b) => a + b, 0) / near.length
          : top;
      };
      column.sort((a, b) => score(a) - score(b));
      column.forEach((node, row) =>
        positions.set(node.id, {
          x: 40 + visiblePhases.indexOf(phase) * 350,
          y: top + (row + (height - column.length) / 2) * 190,
        }),
      );
    });
    top += height * 190 + 70;
  }
  return {
    positions,
    width: visiblePhases.length * 350,
    height: top,
    headings: visiblePhases.map((phase, i) => ({
      ...attackPhases[phase],
      index: phase,
      x: 40 + i * 350,
      y: 10,
    })),
  };
}

export function attackRouteNodes(graph, route) {
  if (!route) return graph.nodes;
  return graph.nodes
    .filter((n) => route.nodeIDs.includes(n.id))
    .map((n) => {
      if (!n.summary) return n;
      const records = n.summary.records.filter((r) =>
        route.recordIDs.includes(r.id),
      );
      const s = {
        ...n.summary,
        records,
        statuses: {},
        delivery: 0,
        download: 0,
        accepted: 0,
        rejected: 0,
        rules: 0,
        first: records[0]?.evidence?.at,
        last: records.at(-1)?.evidence?.at,
      };
      for (const r of records) {
        if (r.evidence?.status != null)
          s.statuses[r.evidence.status] =
            (s.statuses[r.evidence.status] || 0) + 1;
        for (const item of [r, ...r.related]) {
          if (["delivery", "download", "rule"].includes(item.kind))
            s[item.kind === "rule" ? "rules" : item.kind]++;
          if (item.kind === "callback")
            s[item.status === "fail" ? "rejected" : "accepted"]++;
        }
      }
      const failures = records.filter((r) => r.status === "fail").length;
      return {
        ...n,
        summary: s,
        label: records.length === 1 ? records[0].label : n.label,
        kind: s.delivery
          ? "delivery"
          : s.accepted || s.rejected
            ? "callback"
            : s.download
              ? "download"
              : records[0]?.kind || n.kind,
        status:
          failures === records.length
            ? "fail"
            : failures
              ? "mixed"
              : "observed",
        attack: { ...n.attack, phase: Math.max(...records.map(phaseOf)) },
      };
    });
}
