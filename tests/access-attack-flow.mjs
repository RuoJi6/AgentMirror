import test from "node:test";
import assert from "node:assert/strict";
import {
  buildAttackFlow,
  layoutAttackFlow,
  attackRouteNodes,
} from "../frontend/src/features/access-attack-flow.js";

const request = (id, path, extra = {}) => ({
  id: `r${id}`,
  kind: "request",
  group: `s${id}`,
  label: `GET ${path}`,
  status: (extra.status || 200) >= 400 ? "fail" : "observed",
  evidence: {
    method: "GET",
    path,
    status: 200,
    ip: "192.0.2.1",
    ua: "Agent A",
    host: "main.test:8770",
    listener_id: "main",
    listener: { port: 8770 },
    at: new Date(1_000_000 + id * 1000).toISOString(),
    ...extra,
  },
});
function graph(roots, children = []) {
  return buildAttackFlow(
    [
      ...roots,
      ...children.map(([id, kind, status = "observed"]) => ({
        id: `${id}-${kind}`,
        kind,
        group: id,
        label: kind,
        status,
      })),
    ],
    children.map(([id, kind]) => ({
      source: id,
      target: `${id}-${kind}`,
      relation: kind,
    })),
  );
}
const ofRecord = (g, id) =>
  g.nodes.find((n) => n.summary?.records.some((r) => r.id === id));

test("parallel exploration branches share an entry instead of being serialized by first occurrence", () => {
  const g = graph([
    request(1, "/"),
    request(2, "/wiki"),
    request(3, "/notice"),
    request(4, "/login"),
    request(5, "/api/assets"),
    request(6, "/api/status"),
  ]);
  const entry = ofRecord(g, "r1"),
    wiki = ofRecord(g, "r2"),
    notice = ofRecord(g, "r3");
  const root = g.nodes.find((n) => n.kind === "source");
  assert.ok(
    [entry, wiki, notice].every((n) =>
      g.edges.some((e) => e.source === root.id && e.target === n.id),
    ),
  );
  assert.ok(
    !g.edges.some((e) => e.source === wiki.id && e.target === notice.id),
  );
  const auth = ofRecord(g, "r4");
  assert.ok(
    ["r5", "r6"].every((id) =>
      g.edges.some(
        (e) =>
          e.source === auth.id &&
          e.target === ofRecord(g, id).id &&
          e.relation === "inferred_phase",
      ),
    ),
  );
  assert.ok(!g.edges.some((e) => e.relation === "first_seen"));
});

test("trace a specific callback via its own delivery; retain failed response separately from another successful attempt", () => {
  const g = graph(
    [
      request(1, "/"),
      request(2, "/login", {
        referer: "http://main.test:8770/",
        session_id: "s",
      }),
      request(3, "/api/config", { session_id: "s", status: 401 }),
      request(4, "/api/config", { session_id: "s" }),
      request(5, "/custom-callback", { session_id: "s", status: 403 }),
      request(6, "/custom-callback", { session_id: "s", status: 201 }),
    ],
    [
      ["r4", "delivery"],
      ["r5", "callback", "fail"],
      ["r6", "callback"],
    ],
  );
  const callback = g.routes.find((r) => r.id === "r6");
  assert.deepEqual(callback.recordIDs, ["r1", "r2", "r4", "r6"]);
  assert.ok(callback.edges.some((e) => e.relation === "referer"));
  assert.equal(callback.edges.at(-1).relation, "receipt_context");
  const failed = attackRouteNodes(
    g,
    g.routes.find((r) => r.id === "r3"),
  );
  const config = failed.find((n) => n.label === "GET /api/config");
  assert.equal(config.kind, "request");
  assert.equal(config.summary.delivery, 0);
  assert.equal(config.status, "fail");
  assert.deepEqual(config.summary.statuses, { 401: 1 });
  const denied = attackRouteNodes(
    g,
    g.routes.find((r) => r.id === "r5"),
  ).find((n) => n.kind === "callback");
  assert.equal(denied.summary.accepted, 0);
  assert.equal(denied.summary.rejected, 1);
});

test("cross-port links require explicit referer/redirect evidence; UA and idle periods stay separate", () => {
  const main = request(1, "/", {
    status: 302,
    location: "http://child.test:8771/",
  });
  const sub = request(2, "/", {
    host: "child.test:8771",
    listener_id: "child",
    listener: { port: 8771 },
  });
  const unlinked = request(3, "/api/thing", {
    listener_id: "unrelated",
    listener: { port: 8780 },
  });
  const otherAgent = request(4, "/api/config", { ua: "Agent B" });
  const later = request(5, "/api/config", {
    at: new Date(4_000_000).toISOString(),
  });
  const g = graph([main, sub, unlinked, otherAgent, later]);
  assert.equal(g.trails.length, 3);
  assert.ok(
    g.edges.some(
      (e) =>
        e.source === ofRecord(g, "r1").id &&
        e.target === ofRecord(g, "r2").id &&
        e.relation === "redirect",
    ),
  );
  for (const id of ["r3", "r4", "r5"])
    assert.deepEqual(g.routes.find((r) => r.id === id).recordIDs, [id]);
});

test("healthchecks and static assets are reversible background filters; every folded path remains individually traceable", () => {
  const roots = [
    request(1, "/health", { ua: "AgentMirror-Deployment-Healthcheck/1.0" }),
    request(2, "/style.css"),
    request(3, "/one", { status: 404 }),
    request(4, "/two", { status: 404 }),
  ];
  const g = graph(roots);
  assert.equal(g.hiddenCount, 2);
  assert.equal(g.recordCount, 2);
  assert.equal(g.nodes.filter((n) => n.summary).length, 1);
  for (const id of ["r3", "r4"]) {
    const r = g.routes.find((r) => r.id === id);
    const view = attackRouteNodes(g, r).find((n) => n.summary);
    assert.equal(view.summary.records.length, 1);
    assert.equal(view.label, roots.find((n) => n.id === id).label);
  }
  const all = buildAttackFlow(roots, [], { includeBackground: true });
  assert.equal(all.recordCount, 4);
  assert.equal(all.hiddenCount, 0);
});

test("phase layout keeps a bounded width, separated headings and nonoverlapping nodes", () => {
  const g = graph(
    Array.from({ length: 100 }, (_, i) =>
      request(i, i < 20 ? `/login/${i}` : `/api/${i}`),
    ),
  );
  const l = layoutAttackFlow(g.nodes, g.edges);
  assert.equal(l.headings.length, 6);
  assert.ok(l.width <= 2200);
  for (const [a, p] of l.positions)
    for (const [b, q] of l.positions)
      if (a !== b)
        assert.ok(Math.abs(p.x - q.x) >= 250 || Math.abs(p.y - q.y) >= 156);
  assert.ok([...l.positions.values()].every((p) => p.y >= 100));
  const routeLayout = layoutAttackFlow(g.nodes, g.edges, { routeOnly: true });
  assert.deepEqual(
    routeLayout.headings.map((p) => p.index),
    [0, 2, 3],
  );
  assert.equal(routeLayout.width, 1050);
  assert.equal(buildAttackFlow().recordCount, 0);
});
