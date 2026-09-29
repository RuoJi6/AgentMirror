import React, {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Background,
  BackgroundVariant,
  Handle,
  MarkerType,
  MiniMap,
  Panel,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useNodesState,
  useReactFlow,
  useViewport,
} from "@xyflow/react";
import { CircleHelp, Scan, ZoomIn, ZoomOut, RotateCcw } from "lucide-react";
import { Button } from "../components/UI";
import {
  layoutAccessFlow,
  FLOW_NODE_WIDTH,
  FLOW_NODE_HEIGHT,
  flowAncestors,
} from "./access-flow-layout";
import {
  kinds,
  states,
  icons,
  nodeColors,
  relations,
} from "./access-flow-meta";
import "@xyflow/react/dist/style.css";
import { COMPACT_NODE_HEIGHT } from "./access-flow-summary";
import { layoutAttackFlow, attackPhases } from "./access-attack-flow";

const FlowNode = memo(function FlowNode({ data }) {
  const n = data.node,
    Icon = icons[n.kind] || CircleHelp;
  return (
    <div
      className={`flow-node ${data.compact ? "compact" : ""} ${n.kind} ${n.status} ${data.highlight ? "selected" : ""} ${data.dim ? "muted-node" : ""}`}
    >
      {data.compact ? (
        [Position.Left, Position.Right, Position.Top, Position.Bottom].flatMap(
          (position) => [
            <Handle
              key={`in-${position}`}
              id={`in-${position}`}
              type="target"
              position={position}
              isConnectable={false}
            />,
            <Handle
              key={`out-${position}`}
              id={`out-${position}`}
              type="source"
              position={position}
              isConnectable={false}
            />,
          ],
        )
      ) : (
        <Handle type="target" position={Position.Left} isConnectable={false} />
      )}
      <div className="flow-node-type">
        <span
          className="flow-kind-icon"
          style={{
            background: n.attack
              ? attackPhases[n.attack.phase].color
              : nodeColors[n.kind],
          }}
        >
          <Icon size={14} />
        </span>
        <b>
          {n.attack
            ? attackPhases[n.attack.phase].label
            : kinds[n.kind] || n.kind}
        </b>
        <small className={`flow-node-status ${n.status}`}>
          {states[n.status]}
        </small>
      </div>
      <strong title={n.label}>{n.label}</strong>
      {n.summary && (
        <div className="flow-summary-badges">
          {n.summary.delivery > 0 && <span>交付 {n.summary.delivery}</span>}
          {n.summary.download > 0 && <span>下载 {n.summary.download}</span>}
          {n.summary.accepted > 0 && <span>回传接收 {n.summary.accepted}</span>}
          {n.summary.rejected > 0 && (
            <span className="rejected">回传拒绝 {n.summary.rejected}</span>
          )}
          {!n.summary.delivery &&
            !n.summary.download &&
            !n.summary.accepted &&
            !n.summary.rejected && (
              <span
                title={Object.entries(n.summary.statuses)
                  .map(([status, count]) => `${status} × ${count}`)
                  .join(" · ")}
              >
                HTTP {Object.keys(n.summary.statuses).join(" / ") || "未记录"}
              </span>
            )}
        </div>
      )}
      <div className="flow-node-meta">
        <span>
          {n.kind === "source"
            ? n.evidence?.ua
            : n.summary
              ? `${n.summary.records.length} 次访问${n.summary.port ? ` · :${n.summary.port}` : ""}`
              : n.kind === "request"
                ? `HTTP ${n.evidence?.status ?? "—"}${n.evidence?.listener?.port ? ` · :${n.evidence.listener.port}` : ""}`
                : n.kind === "delivery"
                  ? `版本 v${n.evidence?.profile_version ?? "—"}`
                  : "访问证据"}
        </span>
        <span title={n.evidence?.at || n.id}>
          {n.kind === "source"
            ? n.evidence?.ip
            : n.summary
              ? "点击展开"
              : n.evidence?.at
                ? new Date(n.evidence.at).toLocaleTimeString()
                : `#${n.id.replace(/^node-/, "")}`}
        </span>
      </div>
      {!data.compact && (
        <Handle type="source" position={Position.Right} isConnectable={false} />
      )}
    </div>
  );
});
const FlowPhase = memo(function FlowPhase({ data }) {
  return (
    <div className="flow-phase-heading" style={{ borderColor: data.color }}>
      <span style={{ color: data.color }}>{data.index + 1}</span>
      <strong>{data.label}</strong>
    </div>
  );
});
const nodeTypes = { evidence: FlowNode, phase: FlowPhase };
const coordinate = (value) =>
  typeof value === "number" && Number.isFinite(value) && Math.abs(value) < 1e7;
function readView(key) {
  try {
    const raw = JSON.parse(localStorage.getItem(key) || "null");
    if (raw?.version !== 1) return {};
    const positions = Object.fromEntries(
      Object.entries(raw.positions || {})
        .slice(0, 600)
        .filter(([, p]) => p && coordinate(p.x) && coordinate(p.y)),
    );
    const v = raw.viewport;
    return {
      positions,
      viewport:
        v &&
        coordinate(v.x) &&
        coordinate(v.y) &&
        coordinate(v.zoom) &&
        v.zoom >= 0.01 &&
        v.zoom <= 2.5
          ? v
          : undefined,
    };
  } catch {
    return {};
  }
}

function FlowControls({ reset }) {
  const { zoomIn, zoomOut, fitView } = useReactFlow();
  const { zoom } = useViewport();
  return (
    <Panel
      position="bottom-left"
      className="flow-zoom"
      role="group"
      aria-label="画布缩放"
    >
      <Button
        icon={ZoomIn}
        aria-label="放大画板"
        onClick={() => zoomIn({ duration: 150 })}
      />
      <Button
        icon={ZoomOut}
        aria-label="缩小画板"
        onClick={() => zoomOut({ duration: 150 })}
      />
      <Button
        icon={Scan}
        aria-label="适应画布"
        onClick={() =>
          fitView({ padding: 0.25, minZoom: 0.01, maxZoom: 1, duration: 200 })
        }
      />
      <Button icon={RotateCcw} aria-label="重新排列节点" onClick={reset} />
      <span>{Math.round(zoom * 100)}%</span>
    </Panel>
  );
}

function FlowCanvasInner({
  nodes,
  edges,
  selected,
  onSelect,
  canvasRef,
  storageKey,
  compact = false,
  routeOnly = false,
}) {
  const key = `agentmirror.flow-view.v1:${storageKey}`;
  const [saved] = useState(() => (storageKey ? readView(key) : {}));
  const layout = useMemo(
    () =>
      compact
        ? layoutAttackFlow(nodes, edges, { routeOnly })
        : layoutAccessFlow(nodes, edges),
    [nodes, edges, compact, routeOnly],
  );
  const nodeHeight = compact ? COMPACT_NODE_HEIGHT : FLOW_NODE_HEIGHT;
  const ancestry = useMemo(
    () => flowAncestors(selected, edges),
    [selected, edges],
  );
  const asNode = (n) => ({
    id: n.id,
    type: "evidence",
    position: saved.positions?.[n.id] || layout.positions.get(n.id),
    data: { node: n, compact },
    width: FLOW_NODE_WIDTH,
    height: nodeHeight,
    ariaLabel: `${kinds[n.kind]}：${n.label}`,
  });
  const [rendered, setNodes, onNodesChange] = useNodesState(nodes.map(asNode));
  const { fitView, getNodes, getViewport, setCenter } = useReactFlow();
  const initialized = useRef(false);
  const persist = useCallback(
    (view = getViewport()) => {
      if (!initialized.current || !storageKey) return;
      try {
        localStorage.setItem(
          key,
          JSON.stringify({
            version: 1,
            positions: Object.fromEntries(
              getNodes()
                .filter((n) => n.type === "evidence")
                .map((n) => [n.id, n.position]),
            ),
            viewport: view,
          }),
        );
      } catch {
        /* Storage can be unavailable; in-memory interaction still works. */
      }
    },
    [key, storageKey, getNodes, getViewport],
  );
  useEffect(() => {
    setNodes((previous) => {
      const byID = new Map(previous.map((n) => [n.id, n]));
      const occupied = previous
        .filter((n) => nodes.some((item) => item.id === n.id))
        .map((n) => n.position);
      return nodes.map((n) => {
        const old = byID.get(n.id);
        let position =
          old?.position ||
          saved.positions?.[n.id] ||
          layout.positions.get(n.id);
        if (!old) {
          position = { ...position };
          // New polling nodes must not cover nodes the user already moved.
          while (
            occupied.some(
              (p) =>
                Math.abs(p.x - position.x) < FLOW_NODE_WIDTH + 20 &&
                Math.abs(p.y - position.y) < nodeHeight + 20,
            )
          )
            position.y += nodeHeight + 44;
          occupied.push(position);
        }
        return {
          ...old,
          id: n.id,
          type: "evidence",
          position,
          width: FLOW_NODE_WIDTH,
          height: nodeHeight,
          ariaLabel: `${kinds[n.kind]}：${n.label}`,
          data: {
            node: n,
            compact,
            highlight: n.id === selected,
            dim: !!selected && !ancestry.has(n.id),
          },
        };
      });
    });
  }, [nodes, layout, selected, ancestry, saved, setNodes, compact, nodeHeight]);
  const renderedEdges = useMemo(() => {
    const positions = compact
      ? new Map(rendered.map((n) => [n.id, n.position]))
      : new Map();
    return edges.map((e, index) => {
      const meta = relations[e.relation] || {
        color: "#64748b",
        label: e.relation || "关联",
      };
      const highlighted =
        !!selected && ancestry.has(e.source) && ancestry.has(e.target);
      const source = positions.get(e.source);
      const target = positions.get(e.target);
      const vertical =
        source && target && Math.abs(source.x - target.x) < FLOW_NODE_WIDTH;
      const upward = source && target && target.y < source.y;
      const reverse = source && target && target.x < source.x;
      return {
        id: `${e.source}-${e.relation}-${e.target}-${index}`,
        source: e.source,
        target: e.target,
        ...(compact
          ? {
              sourceHandle: `out-${vertical ? (upward ? "top" : "bottom") : reverse ? "left" : "right"}`,
              targetHandle: `in-${vertical ? (upward ? "bottom" : "top") : reverse ? "right" : "left"}`,
            }
          : {}),
        type: compact ? "smoothstep" : "default",
        label: compact && !highlighted ? undefined : meta.label,
        className: `flow-edge ${highlighted ? "selected" : ""}`,
        selectable: false,
        style: {
          stroke: meta.color,
          strokeWidth: highlighted ? 2.8 : 1.8,
          opacity: selected && !highlighted ? 0.2 : 0.75,
          strokeDasharray: meta.dashed ? "6 4" : undefined,
        },
        labelStyle: { fill: meta.color, fontSize: 10, fontWeight: 600 },
        labelShowBg: true,
        labelBgStyle: { fill: "var(--flow-bg)", fillOpacity: 0.88 },
        labelBgPadding: [4, 2],
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: meta.color,
          width: 16,
          height: 16,
        },
      };
    });
  }, [edges, selected, ancestry, compact, rendered, nodeHeight]);
  const legendKinds = [...new Set(nodes.map((n) => n.kind))];
  const legendRelations = [...new Set(edges.map((e) => e.relation))];
  const reset = () => {
    setNodes((previous) =>
      previous.map((n) => ({
        ...n,
        position: layout.positions.get(n.id) || n.position,
      })),
    );
    requestAnimationFrame(() =>
      requestAnimationFrame(() => {
        fitView({ padding: 0.25, minZoom: 0.01, maxZoom: 1, duration: 200 });
        persist();
      }),
    );
  };
  return (
    <ReactFlow
      nodes={
        compact && nodes.length
          ? [
              ...rendered,
              ...layout.headings.map((heading) => ({
                id: `phase-${heading.index}`,
                type: "phase",
                position: { x: heading.x, y: heading.y },
                data: heading,
                width: FLOW_NODE_WIDTH,
                height: 55,
                draggable: false,
                selectable: false,
                focusable: false,
              })),
            ]
          : rendered
      }
      edges={renderedEdges}
      nodeTypes={nodeTypes}
      onNodesChange={(changes) => {
        onNodesChange(changes);
        if (changes.some((c) => c.type === "position" && c.dragging === false))
          requestAnimationFrame(() => persist());
      }}
      onInit={(instance) => {
        canvasRef.current = instance;
        initialized.current = true;
      }}
      onNodeClick={(_, n) => {
        if (n.type === "evidence") onSelect(n.id);
      }}
      onNodeDragStop={() => persist()}
      onMoveEnd={(_, view) => persist(view)}
      onPaneClick={() => onSelect("")}
      nodesDraggable
      nodesConnectable={false}
      edgesReconnectable={false}
      deleteKeyCode={null}
      panOnDrag
      zoomOnScroll
      zoomOnPinch
      selectNodesOnDrag={false}
      nodeDragThreshold={4}
      minZoom={0.01}
      maxZoom={2.5}
      defaultViewport={saved.viewport}
      fitView={!saved.viewport}
      fitViewOptions={{ padding: 0.25, minZoom: 0.01, maxZoom: 1 }}
      proOptions={{ hideAttribution: true }}
      aria-label="访问流程画布：拖动节点调整位置，拖动空白平移，滚轮缩放"
    >
      <Background
        variant={BackgroundVariant.Dots}
        gap={18}
        size={1}
        color="var(--flow-dot)"
      />
      {!compact && (
        <Panel
          position="top-left"
          className="flow-legend"
          aria-label="节点与连线图例"
        >
          <div>
            {legendKinds.map((kind) => {
              const Icon = icons[kind] || CircleHelp;
              return (
                <span key={kind}>
                  <i style={{ background: nodeColors[kind] }}>
                    <Icon size={12} />
                  </i>
                  {kinds[kind] || kind}
                </span>
              );
            })}
          </div>
          <div>
            {legendRelations.map((rel) => {
              const meta = relations[rel] || { label: rel, color: "#64748b" };
              return (
                <span key={rel}>
                  <i
                    className="flow-line-key"
                    style={{
                      borderColor: meta.color,
                      borderTopStyle: meta.dashed ? "dashed" : "solid",
                    }}
                  />
                  {meta.label}
                </span>
              );
            })}
          </div>
        </Panel>
      )}
      <FlowControls reset={reset} />
      <MiniMap
        pannable
        zoomable
        onClick={(_, point) =>
          setCenter(point.x, point.y, {
            zoom: getViewport().zoom,
            duration: 150,
          })
        }
        ariaLabel="画板缩略导航"
        nodeColor={(n) => nodeColors[n.data?.node?.kind] || "#64748b"}
        nodeStrokeWidth={0}
        nodeBorderRadius={3}
        maskColor="rgb(148 163 184 / 0.18)"
      />
    </ReactFlow>
  );
}

export default function AccessFlowCanvas(props) {
  return (
    <ReactFlowProvider>
      <FlowCanvasInner {...props} />
    </ReactFlowProvider>
  );
}
