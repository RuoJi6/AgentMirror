import React, { useEffect, useMemo, useRef, useState } from "react";
import {
  CheckCircle2,
  CircleHelp,
  MousePointer2,
  Target,
  XCircle,
  List,
  Network,
  Play,
  MessageSquare,
  Download,
  PanelRight,
  Maximize2,
  Minimize2,
  X,
} from "lucide-react";
import { api } from "../api";
import { Button } from "../components/UI";
import Select from "../components/Select";
import AccessFlowCanvas from "./AccessFlowCanvas";
import { kinds, states } from "./access-flow-meta";
import { buildAttackFlow, attackRouteNodes } from "./access-attack-flow";
import "./access-flow.css";

const groupTypes = {
  browser: "连续浏览器操作",
  suite: "独立接口检查（构造参数）",
  http: "Agent 单次 HTTP 检查",
  redteam: "红队连续访问与受控复现",
  redteam_browser: "红队浏览器访问与受控复现",
  traffic: "实际访问记录",
};

export function AccessFlowGraph({ flow, storageKey, onSession }) {
  const [groupID, setGroupID] = useState("");
  const [selected, setSelected] = useState("");
  const [mode, setMode] = useState("graph");
  const [details, setDetails] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [simplified, setSimplified] = useState(true);
  const [routeID, setRouteID] = useState("");
  const [includeBackground, setIncludeBackground] = useState(false);
  const canvas = useRef(null),
    shell = useRef(null);
  const groups = flow?.groups || [];
  const observedTraffic = flow?.environment === "observed_traffic";
  const compact = observedTraffic && simplified;
  // New browser groups must not silently replace the larger live graph.
  // Empty selection is an explicit overview of every recorded flow.
  const group = groups.find((g) => g.id === groupID);
  const viewID = `${group?.id || "all"}${compact ? `:attack-v2:${routeID}:${includeBackground}` : ""}`;
  const groupCounts = useMemo(() => {
    const counts = new Map();
    for (const node of flow?.nodes || [])
      counts.set(node.group, (counts.get(node.group) || 0) + 1);
    return counts;
  }, [flow]);
  const rawNodes = useMemo(
    () => (flow?.nodes || []).filter((n) => !group || n.group === group.id),
    [flow, group?.id],
  );
  const rawEdges = useMemo(() => {
    const ids = new Set(rawNodes.map((n) => n.id));
    return (flow?.edges || []).filter(
      (e) => ids.has(e.source) && ids.has(e.target),
    );
  }, [flow, rawNodes]);
  const overview = useMemo(
    () =>
      compact
        ? buildAttackFlow(rawNodes, rawEdges, { includeBackground })
        : null,
    [compact, rawNodes, rawEdges, includeBackground],
  );
  const route = overview?.routes.find((r) => r.id === routeID);
  const nodes = useMemo(
    () => (overview ? attackRouteNodes(overview, route) : rawNodes),
    [overview, route, rawNodes],
  );
  const edges = route?.edges || overview?.edges || rawEdges;
  const node = nodes.find((n) => n.id === selected);
  const deliveries = nodes.filter((n) => n.kind === "delivery");
  useEffect(() => {
    setSelected("");
    setDetails(false);
  }, [viewID]);
  useEffect(() => {
    const update = () =>
      setExpanded(document.fullscreenElement === shell.current);
    document.addEventListener("fullscreenchange", update);
    return () => document.removeEventListener("fullscreenchange", update);
  }, []);
  const select = (id) => {
    setSelected(id);
    if (id) setDetails(true);
  };
  const focus = (id) => {
    select(id);
    const instance = canvas.current,
      target = instance?.getNode(id);
    if (target)
      instance.setCenter(target.position.x + 125, target.position.y + 60, {
        zoom: Math.max(0.8, instance.getZoom()),
        duration: 200,
      });
  };
  const toggleFullscreen = async () => {
    try {
      if (document.fullscreenElement === shell.current)
        await document.exitFullscreen();
      else await shell.current.requestFullscreen();
    } catch {
      /* Fullscreen is optional on embedded/mobile browsers. */
    }
  };
  const exportFlow = () => {
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(flow, null, 2)], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = "access-flow.json";
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };
  if (!flow)
    return (
      <div className="flow-empty">
        此记录尚未生成访问画板。运行一次内容核查或红队测试后，工具执行记录会自动组成访问链路；历史报告保持原样。
      </div>
    );
  return (
    <section ref={shell} className="access-flow" aria-label="访问流程画板">
      <div className="flow-toolbar">
        <Select
          aria-label="选择访问流程"
          value={group?.id || ""}
          onChange={(e) => {
            setGroupID(e.target.value);
            setRouteID("");
          }}
        >
          <option value="">
            全部访问流程 · {flow.nodes?.length || 0} 个节点
          </option>
          {groups.map((g, i) => (
            <option key={g.id} value={g.id}>
              {g.label} · {i + 1} · {groupCounts.get(g.id) || 0} 个节点
            </option>
          ))}
        </Select>
        {observedTraffic && (
          <div
            className="flow-view-switch flow-density-switch"
            role="group"
            aria-label="画板详细程度"
          >
            <Button
              aria-pressed={simplified}
              onClick={() => setSimplified(true)}
            >
              攻击路线
            </Button>
            <Button
              aria-pressed={!simplified}
              onClick={() => setSimplified(false)}
            >
              完整请求
            </Button>
          </div>
        )}
        <div className="flow-view-switch" role="group" aria-label="画板视图">
          <Button
            icon={Network}
            aria-pressed={mode === "graph"}
            onClick={() => setMode("graph")}
          >
            画板
          </Button>
          <Button
            icon={List}
            aria-pressed={mode === "list"}
            onClick={() => setMode("list")}
          >
            列表
          </Button>
        </div>
        <Button
          icon={PanelRight}
          aria-label="节点详情"
          aria-pressed={details}
          onClick={() => setDetails((v) => !v)}
        >
          详情
        </Button>
        {document.fullscreenEnabled && (
          <Button
            icon={expanded ? Minimize2 : Maximize2}
            aria-label={expanded ? "退出全屏画板" : "全屏画板"}
            onClick={toggleFullscreen}
          />
        )}
        <Button icon={Download} onClick={exportFlow}>
          导出
        </Button>
      </div>
      {compact && (
        <div className="flow-route-toolbar">
          <Select
            aria-label="追踪攻击路线"
            value={route?.id || ""}
            onChange={(e) => setRouteID(e.target.value)}
          >
            <option value="">
              全部分支 · {overview.routes.filter((r) => r.terminal).length}{" "}
              条终点路线
            </option>
            {overview.routes
              .filter((r) => r.terminal || r.id === routeID)
              .map((r) => (
                <option key={r.id} value={r.id}>
                  {r.label}
                </option>
              ))}
          </Select>
          {route && (
            <Button onClick={() => setRouteID("")}>返回全部路线</Button>
          )}
          <label>
            <input
              type="checkbox"
              checked={includeBackground}
              onChange={(e) => {
                setIncludeBackground(e.target.checked);
                setRouteID("");
              }}
            />
            显示健康检查与静态资源
            {overview.hiddenCount
              ? `（已折叠 ${overview.hiddenCount} 条）`
              : ""}
          </label>
        </div>
      )}
      <p className="flow-caption">
        {compact ? (
          `本页 ${overview.recordCount} 条访问 · ${overview.trails.length} 个来源时间段 · ${route ? `已追踪 ${route.recordIDs.length} 次访问` : `${nodes.length} 个路线节点`}。按阶段展示分支；实线表示记录中的关联，虚线表示会话时序或阶段推测。阶段分类不代表攻击成功。`
        ) : (
          <>
            {group ? groupTypes[group.kind] || group.label : "全部访问流程"} ·{" "}
            {nodes.length} / {flow.nodes?.length || 0} 个节点 · {edges.length}{" "}
            条连线 · {deliveries.length} 个交付点。
          </>
        )}
        {compact
          ? ""
          : observedTraffic
            ? "虚线只表示本页同一来源的请求时间先后；不证明为同一个 AI，也不表示已执行提示词。"
            : !group
              ? "展示本次任务的全部已记录流程；独立检查之间不会添加顺序连线。"
              : ["redteam", "redteam_browser"].includes(group.kind)
                ? "包含实际响应和合成环境动作；回传接收以隔离记录为准。"
                : group?.kind === "browser"
                  ? "连线表示操作顺序或该操作期间观测到的请求。"
                  : "这些检查不证明已走通登录后的页面入口。"}
        <span className="flow-interaction-hint">
          拖动节点调整位置 · 拖动空白平移 · 滚轮缩放
        </span>
      </p>
      {route && (
        <div className="flow-route-sequence" aria-label="当前路线的访问顺序">
          <span>{route.inferred ? "含推测关联" : "记录中的关联"}</span>
          <ol>
            {route.recordIDs.map((id, i) => {
              const target = nodes.find((n) =>
                n.summary?.records.some((r) => r.id === id),
              );
              const record = target?.summary.records.find((r) => r.id === id);
              return (
                record && (
                  <li key={id}>
                    <button
                      onClick={() => focus(target.id)}
                      title={record.evidence?.at}
                    >
                      <b>{i + 1}</b> {record.label}
                      <small>HTTP {record.evidence?.status ?? "—"}</small>
                    </button>
                  </li>
                )
              );
            })}
          </ol>
        </div>
      )}
      {flow.truncated && (
        <p role="alert">
          画板已达到 600 个节点上限；其余检查请查看完整核查报告。
        </p>
      )}
      <div className={`flow-shell ${details ? "has-inspector" : ""}`}>
        <div className="flow-surface">
          <div className="flow-viewport" hidden={mode !== "graph"}>
            <AccessFlowCanvas
              key={`${storageKey || ""}:${viewID}`}
              nodes={nodes}
              edges={edges}
              compact={compact}
              routeOnly={Boolean(route)}
              selected={selected}
              onSelect={select}
              canvasRef={canvas}
              storageKey={storageKey ? `${storageKey}:${viewID}` : ""}
            />
          </div>
          {mode === "list" && (
            <ol className="flow-list">
              {nodes.map((n) => (
                <li key={n.id}>
                  <button
                    aria-pressed={n.id === selected}
                    onClick={() => select(n.id)}
                  >
                    <small>
                      {kinds[n.kind]} · {states[n.status]}
                      {n.summary && ` · ${n.summary.records.length} 次访问`}
                    </small>
                    <strong>{n.label}</strong>
                  </button>
                </li>
              ))}
            </ol>
          )}
          {!nodes.length && (
            <p className="flow-no-nodes">
              {observedTraffic
                ? "当前筛选下没有访问记录。"
                : "等待 Agent 执行访问工具…"}
            </p>
          )}
        </div>
        {details && (
          <aside className="flow-inspector" aria-label="节点详情">
            <div className="flow-inspector-heading">
              <h3>
                交付终点 <span>{deliveries.length}</span>
              </h3>
              <Button
                icon={X}
                aria-label="关闭节点详情"
                onClick={() => setDetails(false)}
              />
            </div>
            {deliveries.length ? (
              <div className="flow-targets">
                {deliveries.map((n) => (
                  <button
                    key={n.id}
                    onClick={() => focus(n.id)}
                    aria-pressed={n.id === selected}
                  >
                    <Target size={15} />
                    {n.evidence?.profile_name || n.label}
                  </button>
                ))}
              </div>
            ) : (
              <p className="muted">此流程尚未观测到提示词交付。</p>
            )}
            <p className="flow-disclaimer">
              交付表示提示词已出现在响应中，不代表访问者已执行它。
            </p>
            {node ? (
              <div className="flow-detail">
                <h3>{kinds[node.kind]}</h3>
                <strong>{node.label}</strong>
                {!node.summary && (
                  <p className="muted">
                    所属流程：
                    {groups.find((g) => g.id === node.group)?.label ||
                      node.group}
                  </p>
                )}
                <p className={`flow-state ${node.status}`}>
                  {node.status === "pass" ? (
                    <CheckCircle2 size={16} />
                  ) : node.status === "fail" ? (
                    <XCircle size={16} />
                  ) : (
                    <CircleHelp size={16} />
                  )}
                  {states[node.status]}
                </p>
                {node.summary ? (
                  <TrafficSummaryDetail
                    node={node}
                    onSession={onSession}
                    onTrace={compact ? setRouteID : undefined}
                    route={route}
                  />
                ) : (
                  <pre>{JSON.stringify(node.evidence, null, 2)}</pre>
                )}
                {onSession && node.evidence?.session_id && (
                  <Button onClick={() => onSession(node.evidence.session_id)}>
                    打开关联会话
                  </Button>
                )}
                <Button onClick={() => setSelected("")}>取消选择</Button>
              </div>
            ) : (
              <div className="flow-select-hint">
                <MousePointer2 size={22} />
                <p>
                  点击节点查看请求、响应、触发条件与提示词版本。选择交付点可高亮其访问前序。
                </p>
              </div>
            )}
          </aside>
        )}
      </div>
    </section>
  );
}

function TrafficSummaryDetail({ node, onSession, onTrace, route }) {
  const s = node.summary;
  const records = route
    ? s.records.filter((r) => route.recordIDs.includes(r.id))
    : s.records;
  const time = (value) =>
    value ? new Date(value).toLocaleString() : "时间未记录";
  return (
    <div className="flow-summary-detail">
      <p>
        {s.records.length} 次访问{s.port ? ` · 端口 ${s.port}` : ""}
      </p>
      <p className="muted">
        {time(s.first)} — {time(s.last)}
      </p>
      <p>
        {Object.entries(s.statuses)
          .map(([status, count]) => `HTTP ${status} × ${count}`)
          .join(" · ")}
      </p>
      <p>
        交付 {s.delivery} 次 · 下载 {s.download} 次 · 回传接收 {s.accepted} 次 /
        拒绝 {s.rejected} 次
      </p>
      <p className="muted">
        按接口路径合并，查询参数和不同响应保留在下方记录。跨会话记录可分别打开。
      </p>
      {records.map((record, i) => (
        <details key={record.id}>
          <summary>
            <span>
              {i + 1}.{" "}
              {record.evidence?.at
                ? new Date(record.evidence.at).toLocaleTimeString()
                : "历史记录"}{" "}
              · HTTP {record.evidence?.status ?? "—"}
            </span>
            <span>{record.label}</span>
          </summary>
          <pre>
            {JSON.stringify(
              {
                ...record.evidence,
                related: record.related.map((r) => ({
                  kind: r.kind,
                  label: r.label,
                  status: r.status,
                  evidence: r.evidence,
                })),
              },
              null,
              2,
            )}
          </pre>
          {onTrace && (
            <Button onClick={() => onTrace(record.id)}>追踪本次路线</Button>
          )}
          {onSession && record.evidence?.session_id && (
            <Button onClick={() => onSession(record.evidence.session_id)}>
              打开关联会话
            </Button>
          )}
        </details>
      ))}
    </div>
  );
}

export default function WorkspaceAccessFlow({
  workspace,
  dirty,
  onConversation,
}) {
  const [jobs, setJobs] = useState([]),
    [selected, setSelected] = useState(""),
    [record, setRecord] = useState(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const base = `/workspace-reviews/${workspace.id}`;
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  useEffect(() => {
    let live = true,
      timer;
    const poll = async () => {
      try {
        const list = await api(base);
        if (!live) return;
        setJobs(list.items);
        const id = selectedRef.current || list.items[0]?.id;
        if (id) {
          const detail = await api(
            `${base}/flow?job=${encodeURIComponent(id)}`,
          );
          if (!live || (selectedRef.current && selectedRef.current !== id))
            return;
          if (!selectedRef.current) {
            selectedRef.current = id;
            setSelected(id);
          }
          setRecord(detail);
          setError("");
        }
      } catch (e) {
        if (live) setError(e.message);
      } finally {
        if (live) timer = setTimeout(poll, 2500);
      }
    };
    poll();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [base, selected]);
  const start = async () => {
    setBusy(true);
    setError("");
    try {
      const job = await api(base, "POST", {});
      selectedRef.current = job.id;
      setJobs((v) => [job, ...v]);
      setRecord(job);
      setSelected(job.id);
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="workspace-flow composer-stack">
      <div className="flow-heading">
        <div>
          <h2>访问流程画板</h2>
          <p className="muted">
            内容核查 Agent
            访问已保存工作区，自动记录从页面入口到提示词交付的链路。
          </p>
        </div>
        <div className="actions">
          <Button
            icon={MessageSquare}
            disabled={!record?.id}
            onClick={() => onConversation(record.id)}
          >
            在 AI 助手中查看
          </Button>
          <Button
            icon={Play}
            variant="primary"
            disabled={dirty || busy || jobs.some((j) => j.status === "running")}
            onClick={start}
          >
            开始核查并绘图
          </Button>
        </div>
      </div>
      {dirty && <p role="alert">请先保存工作区，再核查最新内容。</p>}
      {error && (
        <p className="composer-error" role="alert">
          {error}
        </p>
      )}
      {jobs.length > 0 && (
        <Select
          aria-label="选择画板核查记录"
          value={selected || jobs[0].id}
          onChange={(e) => {
            selectedRef.current = e.target.value;
            setSelected(e.target.value);
            setRecord(null);
          }}
        >
          {jobs.map((j) => (
            <option key={j.id} value={j.id}>
              {new Date(j.created_at).toLocaleString()} ·{" "}
              {j.agent?.name || "内容核查"} · v{j.workspace_version} ·{" "}
              {j.status === "running"
                ? "执行中"
                : j.status === "completed"
                  ? "已结束"
                  : j.status}
            </option>
          ))}
        </Select>
      )}
      {record?.stale && (
        <p className="flow-stale" role="status">
          工作区或绑定素材已更新。此画板保留的是当时的核查证据，请重新核查以验证当前内容。
        </p>
      )}
      {record?.status === "running" && (
        <p role="status">Agent 正在核查，画板会随访问步骤更新…</p>
      )}
      <AccessFlowGraph
        key={record?.id || "empty"}
        flow={record?.access_flow}
        storageKey={record?.id}
      />
    </div>
  );
}
