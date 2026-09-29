import test from "node:test";
import assert from "node:assert/strict";
import {
  layoutAccessFlow,
  flowAncestors,
  FLOW_NODE_WIDTH,
  FLOW_NODE_HEIGHT,
} from "../frontend/src/features/access-flow-layout.js";

test("browser action branches keep labels apart and trace login before delivery", () => {
  const nodes = [
    "login",
    "auth",
    "open",
    "request",
    "rule",
    "delivery",
    "asset",
  ].map((id) => ({ id }));
  const edges = [
    ["login", "auth", "request"],
    ["login", "open", "next"],
    ["open", "request", "request"],
    ["request", "rule", "matched"],
    ["rule", "delivery", "delivered"],
    ["open", "asset", "request"],
  ].map(([source, target, relation]) => ({ source, target, relation }));
  const { positions } = layoutAccessFlow(nodes, edges);
  for (const edge of edges)
    assert.ok(
      positions.get(edge.target).x > positions.get(edge.source).x,
      "every causal/temporal edge should progress left to right",
    );
  for (const [a, p] of positions)
    for (const [b, q] of positions) {
      if (a === b) continue;
      assert.ok(
        Math.abs(p.x - q.x) >= FLOW_NODE_WIDTH ||
          Math.abs(p.y - q.y) >= FLOW_NODE_HEIGHT,
        `${a} overlaps ${b}`,
      );
    }
  assert.deepEqual(
    [...flowAncestors("delivery", edges)].sort(),
    ["delivery", "login", "open", "request", "rule"].sort(),
  );
  assert.deepEqual(
    layoutAccessFlow(nodes, edges),
    layoutAccessFlow(nodes, edges),
  );
});

test("shared descendants and the 600-node limit keep finite non-overlapping forward layers", () => {
  const nodes = Array.from({ length: 600 }, (_, i) => ({ id: String(i) }));
  const edges = nodes.slice(1).flatMap((n, i) => [
    { source: String(Math.floor(i / 3)), target: n.id, relation: "next" },
    ...(i > 0
      ? [
          {
            source: String(Math.floor((i - 1) / 3)),
            target: n.id,
            relation: "request",
          },
        ]
      : []),
  ]);
  const { positions, width, height } = layoutAccessFlow(nodes, edges);
  assert.equal(positions.size, 600);
  assert.ok(Number.isFinite(width) && Number.isFinite(height));
  for (const e of edges)
    assert.ok(positions.get(e.source).x < positions.get(e.target).x);
  const placed = [...positions.values()];
  for (let i = 0; i < placed.length; i++)
    for (let j = i + 1; j < placed.length; j++) {
      assert.ok(
        Math.abs(placed[i].x - placed[j].x) >= FLOW_NODE_WIDTH ||
          Math.abs(placed[i].y - placed[j].y) >= FLOW_NODE_HEIGHT,
      );
    }
});

test("old empty records and malformed cyclic input remain finite", () => {
  assert.equal(layoutAccessFlow().positions.size, 0);
  const edges = [
    { source: "a", target: "b" },
    { source: "b", target: "a" },
    { source: "missing", target: "a" },
  ];
  const layout = layoutAccessFlow([{ id: "a" }, { id: "b" }], edges);
  assert.equal(layout.positions.size, 2);
  assert.ok(Number.isFinite(layout.height));
});
