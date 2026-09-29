export const FLOW_NODE_WIDTH = 250;
export const FLOW_NODE_HEIGHT = 120;

// Layer the entire evidence DAG left-to-right, including temporal edges.
// Break DFS back edges only for layout; every original edge remains visible.
export function layoutAccessFlow(nodes = [], edges = []) {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const outgoing = new Map(nodes.map((n) => [n.id, []]));
  for (const e of edges) {
    if (byId.has(e.source) && byId.has(e.target))
      outgoing.get(e.source).push(e.target);
  }
  const state = new Map(),
    order = [],
    parents = new Map(),
    children = new Map();
  for (const { id } of nodes) {
    if (state.has(id)) continue;
    const stack = [{ id, index: 0 }];
    state.set(id, 1);
    while (stack.length) {
      const frame = stack.at(-1),
        next = outgoing.get(frame.id);
      if (frame.index === next.length) {
        state.set(frame.id, 2);
        order.push(frame.id);
        stack.pop();
        continue;
      }
      const child = next[frame.index++];
      if (state.get(child) === 1) continue;
      parents.set(child, [...(parents.get(child) || []), frame.id]);
      children.set(frame.id, [...(children.get(frame.id) || []), child]);
      if (!state.has(child)) {
        state.set(child, 1);
        stack.push({ id: child, index: 0 });
      }
    }
  }
  const depth = new Map(),
    columns = [];
  for (const id of order.reverse()) {
    const level = Math.max(
      0,
      ...(parents.get(id) || []).map((p) => (depth.get(p) || 0) + 1),
    );
    depth.set(id, level);
  }
  for (const { id } of nodes) (columns[depth.get(id)] ||= []).push(id);
  const rows = new Map();
  const indexRows = () =>
    columns.forEach((col) => col.forEach((id, i) => rows.set(id, i)));
  indexRows();
  // Barycentric ordering reduces crossings without changing evidence ancestry.
  for (let pass = 0; pass < 4; pass++) {
    const sequence = pass % 2 ? [...columns].reverse() : columns;
    const neighbors = pass % 2 ? children : parents;
    for (const col of sequence) {
      const score = (id) => {
        const near = neighbors.get(id) || [];
        return near.length
          ? near.reduce((sum, p) => sum + rows.get(p), 0) / near.length
          : rows.get(id);
      };
      col.sort((a, b) => score(a) - score(b));
      col.forEach((id, i) => rows.set(id, i));
    }
  }
  const positions = new Map();
  const count = Math.max(1, ...columns.map((col) => col.length));
  columns.forEach((col, x) =>
    col.forEach((id, y) =>
      positions.set(id, {
        x: 40 + x * 350,
        y: 40 + ((count - col.length) / 2 + y) * 164,
      }),
    ),
  );
  return {
    positions,
    width: Math.max(560, columns.length * 350 + 40),
    height: Math.max(380, count * 164 + 40),
  };
}

export function flowAncestors(id, edges) {
  const result = new Set(id ? [id] : []);
  const incoming = new Map();
  for (const e of edges) {
    if (!incoming.has(e.target)) incoming.set(e.target, []);
    incoming.get(e.target).push(e.source);
  }
  const queue = [...result];
  for (let i = 0; i < queue.length; i++) {
    for (const parent of incoming.get(queue[i]) || []) {
      if (!result.has(parent)) {
        result.add(parent);
        queue.push(parent);
      }
    }
  }
  return result;
}
