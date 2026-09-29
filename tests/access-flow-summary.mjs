import test from "node:test";
import assert from "node:assert/strict";
import {
  summarizeAccessFlow,
  layoutAccessSummary,
  COMPACT_NODE_HEIGHT,
} from "../frontend/src/features/access-flow-summary.js";
import { FLOW_NODE_WIDTH } from "../frontend/src/features/access-flow-layout.js";

const request = (id, path, status = 200, extra = {}) => ({
  id: String(id),
  kind: "request",
  label: `GET ${path}`,
  group: "session-a",
  status: status >= 400 ? "fail" : "observed",
  evidence: {
    method: "GET",
    path,
    status,
    listener_id: "main",
    listener: { port: 8770 },
    at: `2026-09-19T10:00:${String(id).padStart(2, "0")}Z`,
    session_id: "session-a",
    ...extra,
  },
});
const child = (parent, kind, status = "observed", extra = {}) => ({
  id: `${parent.id}-${kind}`,
  group: parent.group,
  kind,
  label: kind,
  status,
  evidence: { ...parent.evidence, ...extra },
});

test("repeated routes and scanning collapse without dropping records, query values or response variants", () => {
  const nodes = Array.from({ length: 100 }, (_, i) =>
    request(
      i,
      i < 40
        ? `/login?attempt=${i}`
        : i < 70
          ? `/docs?x=${i}`
          : `/missing-${i}`,
      i < 20 ? 401 : i < 70 ? 200 : 404,
      { method: i < 40 ? "POST" : "GET" },
    ),
  );
  const original = JSON.stringify(nodes);
  const summary = summarizeAccessFlow(nodes);
  assert.equal(summary.recordCount, 100);
  assert.equal(summary.nodes.length, 3);
  const login = summary.nodes[0];
  assert.equal(login.label, "POST /login");
  assert.equal(login.status, "mixed");
  assert.deepEqual(login.summary.statuses, { 200: 20, 401: 20 });
  assert.equal(login.summary.records[19].evidence.path, "/login?attempt=19");
  assert.equal(summary.nodes[2].summary.records.length, 30);
  assert.equal(JSON.stringify(nodes), original);
  assert.deepEqual(
    summary.nodes.flatMap((n) => n.summary.records.map((r) => r.id)).sort(),
    nodes.map((n) => n.id).sort(),
  );
  assert.equal(summary.edges.length, 2);
  assert.ok(summary.edges.every((e) => e.relation === "first_seen"));
});

test("preserve each delivery version, download, rule, accepted/rejected callback and legacy event", () => {
  const docs = request(1, "/docs.js", 404),
    next = request(2, "/docs.js", 200);
  const denied = request(3, "/custom-receipt", 403),
    accepted = request(4, "/custom-receipt", 201);
  const file = request(5, "/bundle.js", 200),
    legacy = { ...request(6, "/old"), kind: "delivery" };
  const roots = [docs, next, denied, accepted, file, legacy];
  const children = [
    child(docs, "delivery", "observed", { profile_version: 1 }),
    child(next, "delivery", "observed", { profile_version: 2 }),
    child(docs, "rule"),
    child(denied, "callback", "fail"),
    child(accepted, "callback"),
    child(file, "download"),
  ];
  const all = [...roots, ...children];
  const edges = children.map((n) => ({
    source: n.id.split("-")[0],
    target: n.id,
    relation: n.kind,
  }));
  const s = summarizeAccessFlow(all, edges);
  assert.equal(s.recordCount, 6);
  assert.equal(s.nodes.length, 4);
  assert.equal(s.nodes[0].summary.delivery, 2);
  assert.equal(s.nodes[0].summary.rules, 1);
  assert.deepEqual(
    s.nodes[0].summary.records.flatMap((n) =>
      n.related
        .filter((c) => c.kind === "delivery")
        .map((c) => c.evidence.profile_version),
    ),
    [1, 2],
  );
  assert.equal(s.nodes[1].summary.accepted, 1);
  assert.equal(s.nodes[1].summary.rejected, 1);
  assert.equal(s.nodes[1].status, "mixed");
  assert.equal(s.nodes[2].kind, "download");
  assert.equal(s.nodes[3].summary.delivery, 1);
  assert.equal(
    s.nodes.flatMap((n) => n.summary.records.flatMap((r) => [r, ...r.related]))
      .length,
    all.length,
  );
});

test("separate methods/ports/hosts/workspaces and preserve raw traversal paths; static requests are explicit folds", () => {
  const nodes = [
    request(1, "/login"),
    request(2, "/login", 200, { method: "POST" }),
    request(3, "/login", 200, {
      listener_id: "subsite",
      listener: { port: 8771 },
    }),
    request(4, "/login", 200, { host: "another.example" }),
    request(5, "/login", 200, { deployment_id: "other" }),
    request(6, "/a/../login"),
    request(7, "/style.css"),
    request(8, "/app.js"),
    request(9, "/app.js", 500),
  ];
  const s = summarizeAccessFlow(nodes);
  assert.equal(s.nodes.length, 8);
  assert.equal(
    s.nodes.filter((n) => n.summary.noise === "static")[0].summary.records
      .length,
    2,
  );
  assert.ok(s.nodes.some((n) => n.label === "GET /a/../login"));
  assert.ok(
    s.nodes.some((n) => n.status === "fail" && n.summary.records.length === 1),
  );
  assert.deepEqual(summarizeAccessFlow(), {
    nodes: [],
    edges: [],
    recordCount: 0,
  });
});

test("summary layout wraps at four columns, has no overlaps, keeps stable groups when the latest page rolls", () => {
  const nodes = Array.from({ length: 100 }, (_, i) => request(i, `/api/${i}`));
  const summary = summarizeAccessFlow(nodes);
  const { positions, width } = layoutAccessSummary(summary.nodes);
  assert.ok(width <= 1280);
  const values = [...positions.values()];
  for (let i = 0; i < values.length; i++)
    for (let j = i + 1; j < values.length; j++)
      assert.ok(
        Math.abs(values[i].x - values[j].x) >= FLOW_NODE_WIDTH ||
          Math.abs(values[i].y - values[j].y) >= COMPACT_NODE_HEIGHT,
      );
  const previous = summarizeAccessFlow([
    request(1, "/login"),
    request(2, "/docs"),
  ]);
  const refreshed = summarizeAccessFlow([
    request(2, "/docs"),
    request(3, "/login"),
  ]);
  assert.equal(previous.nodes[0].id, refreshed.nodes[1].id);
  assert.equal(previous.nodes[1].id, refreshed.nodes[0].id);
});
