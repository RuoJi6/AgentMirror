import Select from "../components/Select";
import React, { lazy, Suspense, useEffect, useRef, useState } from "react";
import {
  Download,
  Trash2,
  Search,
  RefreshCw,
  ArrowLeft,
  Save,
  ChevronLeft,
  ChevronRight,
  Copy,
  Eye,
  Clock,
  FileJson,
  Activity,
  CheckCircle2,
  Network,
} from "lucide-react";
import { useApp } from "../App";
import { api, download, formatTime, carrierNames } from "../api";
import {
  Button,
  PageHeader,
  Panel,
  Field,
  Confirm,
  Badge,
  Callout,
  JsonView,
  Empty,
  CopyButton,
  Modal,
} from "../components/UI";
import TextContent from "../components/TextContent";
import FlowErrorBoundary from "./FlowErrorBoundary";
const SessionSources = lazy(() => import("./SessionSources"));
const eventLabels = {
  visit: "访问页面",
  delivery: "交付提示词",
  request: "请求资源",
  interaction: "完成模拟登录",
  receipt: "收到回传",
  annotation: "人工标记",
  rule_matched: "模拟规则命中",
  response_sent: "响应已发送",
  file_download: "文件已下载",
};
const outcomeLabels = {
  unknown: "未判定",
  refused: "已拒绝（人工标记）",
  executed: "已执行（人工标记）",
};
function ReportContent({ sessionId, reportId, onClose }) {
  const [report, setReport] = useState(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let current = true;
    api(`/sessions/${sessionId}/reports/${reportId}`)
      .then((value) => {
        if (current) setReport(value);
      })
      .catch((e) => {
        if (current) setError(e.message);
      });
    return () => {
      current = false;
    };
  }, [sessionId, reportId]);
  const downloadRaw = () => {
    const url = URL.createObjectURL(
      new Blob([report.content], { type: "text/plain;charset=utf-8" }),
    );
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `report-${reportId}.txt`;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };
  return (
    <Modal
      title={`回传 #${reportId} · 内容`}
      wide
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>关闭</Button>
          <Button icon={Download} disabled={!report} onClick={downloadRaw}>
            下载原始内容
          </Button>
        </>
      }
    >
      {error ? (
        <Callout>{error}</Callout>
      ) : report ? (
        <>
          <p className="muted">
            内容按文本显示，不执行 HTML
            或脚本。长内容分段查看，原始内容可完整下载。
          </p>
          <TextContent text={report.content} label="回传完整内容" expanded />
        </>
      ) : (
        <p role="status">正在加载回传内容…</p>
      )}
    </Modal>
  );
}

function SessionDetail({ id }) {
  const { navigate, notify, mutate } = useApp();
  const [session, setSession] = useState(null),
    [tab, setTab] = useState("reports"),
    [outcome, setOutcome] = useState("unknown"),
    [note, setNote] = useState(""),
    [error, setError] = useState(""),
    [reportPage, setReportPage] = useState(1),
    [selectedReport, setSelectedReport] = useState(null);
  const requestVersion = useRef(0);
  const load = async () => {
    const currentRequest = ++requestVersion.current;
    try {
      const s = await api("/sessions/" + id + "?reports_page=" + reportPage);
      if (currentRequest !== requestVersion.current) return;
      const lastPage = Math.max(1, Math.ceil(s.report_count / 5));
      if (reportPage > lastPage) {
        setReportPage(lastPage);
        return;
      }
      setSession(s);
      setOutcome(s.outcome);
      setNote(s.note);
      setError("");
    } catch (e) {
      if (currentRequest !== requestVersion.current) return;
      setError(e.message);
    }
  };
  useEffect(() => {
    load();
    return () => {
      requestVersion.current++;
    };
  }, [id, reportPage]);
  if (error)
    return (
      <>
        <Button icon={ArrowLeft} onClick={() => navigate("sessions")}>
          返回会话列表
        </Button>
        <Empty title={error} />
      </>
    );
  if (!session) return <Empty title="正在加载会话…" />;
  const snap = session.snapshot,
    delivered = session.delivered,
    reportCount = session.report_count;
  const composed = snap.deployment.mode === "composable";
  const profiles = composed
    ? [
        ...new Map(
          (snap.rules || [])
            .filter((rule) => rule.profile?.id)
            .map((rule) => [
              `${rule.profile.id}:${rule.profile.version}`,
              rule.profile,
            ]),
        ).values(),
      ]
    : [snap.profile].filter(Boolean);
  const profileSummary =
    profiles
      .map((profile) => `${profile.name} · v${profile.version}`)
      .join(" / ") || "此会话未绑定提示词";
  const deliveries = session.events.filter(
    (event) =>
      event.kind === "delivery" &&
      (event.detail.body !== undefined ||
        event.detail.instruction !== undefined),
  );
  const currentProfileSummary =
    (session.current_profiles || [])
      .map(
        (profile) =>
          `${profile.name} · v${profile.version}${profile.available === false ? "（方案已删除，使用快照）" : ""}`,
      )
      .join(" / ") || "未绑定提示词";
  return (
    <>
      <button className="back-link" onClick={() => navigate("sessions")}>
        <ArrowLeft size={16} />
        返回会话列表
      </button>
      <PageHeader
        title={snap.deployment.name}
        description={`会话 ${session.id} · ${snap.listener ? `${snap.listener.name} :${snap.listener.port}` : "主测试端口（历史记录）"} · 创建于 ${formatTime(session.created_at)}`}
      >
        <Button
          icon={Network}
          onClick={() =>
            navigate("sessions", `ip:${encodeURIComponent(session.ip)}`)
          }
        >
          同 IP 探索画板
        </Button>
        <Button icon={RefreshCw} onClick={load}>
          刷新
        </Button>
        <Button
          icon={Download}
          onClick={() => {
            const anchor = document.createElement("a");
            anchor.href = "/api/sessions/" + id + "?download=1";
            anchor.download = "session-" + id + ".json";
            anchor.click();
          }}
        >
          导出会话
        </Button>
      </PageHeader>
      <div className="session-facts">
        <div>
          <small>当前请求使用的提示词</small>
          <strong>{currentProfileSummary}</strong>
        </div>
        <div>
          <small>访问来源</small>
          <strong className="mono">{session.ip}</strong>
        </div>
        <div>
          <small>观测状态</small>
          <strong>
            <Badge tone={reportCount ? "green" : "purple"}>
              {reportCount
                ? "已收到回传"
                : delivered
                  ? "已交付提示词"
                  : "已访问"}
            </Badge>
          </strong>
        </div>
        <div>
          <small>人工结论</small>
          <strong>{outcomeLabels[session.outcome]}</strong>
        </div>
      </div>
      <div className="session-layout">
        <div>
          <Panel className="session-content">
            <div className="tabs">
              {[
                ["reports", "回传数据"],
                ["snapshot", "投放快照"],
                ["client", "请求信息"],
              ].map(([key, label]) => (
                <button
                  className={tab === key ? "active" : ""}
                  key={key}
                  onClick={() => setTab(key)}
                >
                  {label}
                  {key === "reports" && <small>{reportCount}</small>}
                </button>
              ))}
            </div>
            <div className="detail-body">
              {tab === "reports" && (
                <>
                  {session.reports.length ? (
                    session.reports.map((report) => (
                      <section className="report" key={report.id}>
                        <div className="section-toolbar">
                          <h3>回传 #{report.id}</h3>
                          <small>{formatTime(report.at)}</small>
                        </div>
                        <TextContent
                          text={report.preview}
                          label={`回传 #${report.id} 摘要`}
                        />
                        <div className="report-summary-actions">
                          <span>
                            {Number(report.bytes).toLocaleString()} 字节
                            {report.characters > 600
                              ? " · 摘要显示前 600 字符"
                              : ""}
                          </span>
                          <Button
                            icon={Eye}
                            onClick={() => setSelectedReport(report.id)}
                          >
                            {report.characters > 600
                              ? "查看完整回传"
                              : "查看内容"}
                          </Button>
                        </div>
                      </section>
                    ))
                  ) : (
                    <Empty
                      title="等待客户端回传"
                      description="客户端提交结果后在此展示，可接收自由 JSON 内容或带会话标识的文本。"
                    />
                  )}
                  {reportCount > 5 && (
                    <div className="content-controls report-pagination">
                      <Button
                        disabled={reportPage <= 1}
                        onClick={() => setReportPage(reportPage - 1)}
                      >
                        上一页回传
                      </Button>
                      <span>
                        第 {reportPage} / {Math.ceil(reportCount / 5)} 页 · 共{" "}
                        {reportCount} 次
                      </span>
                      <Button
                        disabled={reportPage * 5 >= reportCount}
                        onClick={() => setReportPage(reportPage + 1)}
                      >
                        下一页回传
                      </Button>
                    </div>
                  )}
                </>
              )}
              {tab === "snapshot" && (
                <>
                  <div className="detail-pairs">
                    {snap.surface && (
                      <>
                        <span>暴露面</span>
                        <strong>{carrierNames[snap.surface.kind]}</strong>
                      </>
                    )}
                    <span>页面模板</span>
                    <strong>{snap.template.name}</strong>
                    <span>创建时提示词版本</span>
                    <strong>{profileSummary}</strong>
                    <span>当前提示词版本</span>
                    <strong>{currentProfileSummary}</strong>
                    <span>投放时机</span>
                    <strong>
                      {composed
                        ? "按请求规则匹配"
                        : snap.surface
                          ? "直接访问暴露面"
                          : snap.deployment.trigger === "after_login"
                            ? "模拟登录后"
                            : "页面加载时"}
                    </strong>
                    <span>投放载体</span>
                    <strong>
                      {composed
                        ? "自定义 HTTP 响应"
                        : snap.deployment.carriers
                            ?.map((c) => carrierNames[c] || c)
                            .join(" · ")}
                    </strong>
                    <span>创建时回传地址</span>
                    <code>{snap.callback_url}</code>
                    <span>当前回传地址</span>
                    <code>
                      {session.current_callback_url || "原端口已解绑"}
                    </code>
                    <span>接入端口</span>
                    <code>
                      {snap.listener
                        ? `${snap.listener.name} · ${snap.listener.host}:${snap.listener.port}`
                        : "主测试端口（历史记录）"}
                    </code>
                  </div>
                  {composed ? (
                    <>
                      <p className="muted">
                        提示词随保存热更新；下方为创建时快照，实际使用的版本见每条交付记录。
                      </p>
                      <h3>创建时场景与提示词版本</h3>
                      {(snap.rules || []).map((rule) => (
                        <div
                          className="composer-session-rule"
                          key={`${rule.binding_id}:${rule.id}`}
                        >
                          <strong>
                            {rule.scenario_name} · v{rule.scenario_version} /{" "}
                            {rule.name || rule.id}
                          </strong>
                          <code>
                            {rule.method} {rule.path}
                          </code>
                          <span>
                            {rule.profile
                              ? `${rule.profile.name} · v${rule.profile.version}`
                              : "普通响应，无提示词"}
                          </span>
                        </div>
                      ))}
                      <details>
                        <summary>
                          创建时提示词快照（不代表当前版本或已交付）
                        </summary>
                        {profiles.map((profile) => (
                          <div key={`${profile.id}:${profile.version}`}>
                            <h4>
                              {profile.name} · v{profile.version}
                            </h4>
                            <JsonView value={profile.body} />
                          </div>
                        ))}
                      </details>
                    </>
                  ) : (
                    <>
                      <div className="section-toolbar">
                        <h3>创建时提示词正文</h3>
                        <CopyButton value={snap.instruction} notify={notify} />
                      </div>
                      <JsonView value={snap.instruction} />
                    </>
                  )}
                  <h3>已记录的交付内容</h3>
                  {deliveries.length ? (
                    deliveries.map((event) => (
                      <details key={event.id}>
                        <summary>
                          {event.detail.path || event.detail.rule_id} ·{" "}
                          {formatTime(event.at)}
                          {(event.detail.deliveries || []).map(
                            (delivery, index) => (
                              <span key={index}>
                                {" "}
                                · {delivery.profile_name ||
                                  delivery.profile_id}{" "}
                                · v{delivery.profile_version}
                              </span>
                            ),
                          )}
                        </summary>
                        <JsonView
                          value={event.detail.body ?? event.detail.instruction}
                        />
                      </details>
                    ))
                  ) : (
                    <p className="muted">
                      当前时间线没有记录交付正文；完整事件可通过导出会话查看。
                    </p>
                  )}
                  <details>
                    <summary>完整配置快照</summary>
                    <JsonView value={snap} />
                  </details>
                </>
              )}
              {tab === "client" && (
                <div className="detail-pairs">
                  <span>会话编号</span>
                  <code>{session.id}</code>
                  <span>创建时间</span>
                  <strong>{formatTime(session.created_at)}</strong>
                  <span>源 IP</span>
                  <code>{session.ip}</code>
                  <span>User-Agent</span>
                  <code>{session.ua || "未提供"}</code>
                </div>
              )}
            </div>
          </Panel>
          <Panel title="实验结论" className="annotation-panel">
            <Callout>
              GET 或 POST 只能证明请求发生。是否执行命令、是否拒绝，应结合被测
              Agent 的日志人工判断。
            </Callout>
            <div className="form-grid">
              <Field label="人工标记">
                <Select
                  value={outcome}
                  onChange={(e) => setOutcome(e.target.value)}
                >
                  {Object.entries(outcomeLabels).map(([k, v]) => (
                    <option key={k} value={k}>
                      {v}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label="证据或备注">
                <input
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  placeholder="记录日志中的执行或拒绝证据"
                />
              </Field>
            </div>
            <Button
              icon={Save}
              onClick={async () => {
                const r = await mutate(
                  "/sessions/" + id + "/label",
                  "POST",
                  { outcome, note },
                  "结论已保存",
                );
                if (r) load();
              }}
            >
              保存结论
            </Button>
          </Panel>
        </div>
        <Panel title="事件时间线" className="timeline-panel">
          {session.event_count > 100 && (
            <p className="muted">显示最近 100 个事件；导出会话可查看全部。</p>
          )}
          <ol className="event-list">
            {session.events.map((event) => (
              <li key={event.id}>
                <span
                  className={`event-dot ${event.kind === "receipt" ? "green" : ""}`}
                />
                <div>
                  <strong>{eventLabels[event.kind] || event.kind}</strong>
                  <small>{formatTime(event.at)}</small>
                  {event.detail.path && <code>{event.detail.path}</code>}
                  {event.detail.carriers?.length > 0 && (
                    <p>
                      {event.detail.carriers
                        .map((c) => carrierNames[c] || c)
                        .join(" · ")}
                    </p>
                  )}
                  {event.kind === "rule_matched" && (
                    <>
                      <code>{event.detail.rule_id}</code>
                      <small>实例 {event.detail.binding_id}</small>
                    </>
                  )}
                  {event.kind === "response_sent" && (
                    <p>
                      HTTP {event.detail.status} · {event.detail.content_type}
                    </p>
                  )}
                  {event.kind === "file_download" && (
                    <p>
                      {event.detail.filename} · {event.detail.bytes_sent} /{" "}
                      {event.detail.size} 字节
                      {event.detail.range ? ` · ${event.detail.range}` : ""}
                    </p>
                  )}
                  {event.detail.locked && <Badge>登录前 · 未投放</Badge>}
                  {event.detail.note && <p>{event.detail.note}</p>}
                </div>
              </li>
            ))}
          </ol>
        </Panel>
      </div>
      {selectedReport !== null && (
        <ReportContent
          key={selectedReport}
          sessionId={id}
          reportId={selectedReport}
          onClose={() => setSelectedReport(null)}
        />
      )}
    </>
  );
}
export default function Sessions() {
  const { data, route, navigate, notify, mutate } = useApp();
  const [list, setList] = useState({
      items: [],
      total: 0,
      page: 1,
      page_size: 20,
    }),
    [search, setSearch] = useState(""),
    [status, setStatus] = useState(""),
    [deployment, setDeployment] = useState(""),
    [page, setPage] = useState(1),
    [clearing, setClearing] = useState(false),
    [busy, setBusy] = useState(false),
    [loading, setLoading] = useState(true),
    [error, setError] = useState("");
  const load = async () => {
    setLoading(true);
    try {
      const q = new URLSearchParams({
        search,
        status,
        deployment,
        page: String(page),
      });
      const result = await api("/sessions?" + q);
      setList(result);
      setError("");
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    let active = true;
    const timer = setTimeout(async () => {
      setLoading(true);
      try {
        const result = await api(
          "/sessions?" +
            new URLSearchParams({
              search,
              status,
              deployment,
              page: String(page),
            }),
        );
        if (active) {
          setList(result);
          setError("");
        }
      } catch (e) {
        if (active) setError(e.message);
      } finally {
        if (active) setLoading(false);
      }
    }, 180);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [search, status, deployment, page, route.id]);
  if (route.id === "ip" || route.id?.startsWith("ip:")) {
    let ip;
    try {
      ip =
        route.id === "ip" ? undefined : decodeURIComponent(route.id.slice(3));
    } catch {
      return <Empty title="来源 IP 地址无效" />;
    }
    return (
      <FlowErrorBoundary key={route.id}>
        <Suspense fallback={<Empty title="正在加载来源画板…" />}>
          <SessionSources key={route.id} ip={ip} />
        </Suspense>
      </FlowErrorBoundary>
    );
  }
  if (route.id) return <SessionDetail key={route.id} id={route.id} />;
  return (
    <>
      <PageHeader
        title="会话与回传"
        description="沿着请求、投放和回传，查看每次实验的完整记录。"
      >
        <Button icon={Network} onClick={() => navigate("sessions", "ip")}>
          IP 探索画板
        </Button>
        <Button icon={RefreshCw} loading={loading} onClick={load}>
          刷新
        </Button>
        <Button
          icon={Download}
          onClick={async () => {
            try {
              download("agentmirror-sessions.json", await api("/export"));
            } catch (e) {
              notify(e.message, "error");
            }
          }}
        >
          导出记录
        </Button>
        <Button
          icon={Trash2}
          variant="danger-outline"
          onClick={() => setClearing(true)}
        >
          清除记录
        </Button>
      </PageHeader>
      <Panel>
        <div className="filter-bar">
          <div className="search-input">
            <Search size={16} />
            <input
              aria-label="搜索会话"
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
                setPage(1);
              }}
              placeholder="搜索编号、IP 或 User-Agent…"
            />
          </div>
          <Select
            aria-label="筛选状态"
            value={status}
            onChange={(e) => {
              setStatus(e.target.value);
              setPage(1);
            }}
          >
            <option value="">全部状态</option>
            <option value="visited">已访问</option>
            <option value="delivered">已投放，未回传</option>
            <option value="received">已回传</option>
            <option value="refused">人工标记拒绝</option>
          </Select>
          <Select
            aria-label="筛选部署"
            value={deployment}
            onChange={(e) => {
              setDeployment(e.target.value);
              setPage(1);
            }}
          >
            <option value="">全部工作区与历史部署</option>
            {data.deployments.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
            {(data.legacy_deployments || []).map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}（历史记录）
              </option>
            ))}
          </Select>
        </div>
        {error ? (
          <Empty title={error} />
        ) : list.items.length ? (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>会话 / 部署</th>
                  <th>创建时提示词版本</th>
                  <th>端口 / 访问来源</th>
                  <th>创建时间</th>
                  <th>交付 / 回传</th>
                  <th>状态</th>
                </tr>
              </thead>
              <tbody>
                {list.items.map((s) => (
                  <tr key={s.id}>
                    <td>
                      <button
                        className="table-link"
                        onClick={() => navigate("sessions", s.id)}
                      >
                        <strong>
                          {s.deployment_name}
                          {s.surface
                            ? " / " + carrierNames[s.surface.kind]
                            : ""}
                        </strong>
                        <small className="mono">{s.id}</small>
                      </button>
                    </td>
                    <td>
                      {s.profiles?.length ? (
                        <>
                          {s.profiles.map((profile) => (
                            <small
                              className="cell-sub"
                              key={`${profile.id}:${profile.version}`}
                            >
                              {profile.name} · v{profile.version}
                            </small>
                          ))}
                        </>
                      ) : s.mode === "composable" ||
                        data.deployments.find((d) => d.id === s.deployment_id)
                          ?.mode === "composable" ? (
                        <>
                          <span>场景绑定快照</span>
                          <small className="cell-sub">
                            查看详情中的各方案版本
                          </small>
                        </>
                      ) : (
                        <>
                          {s.profile_name}
                          <small className="cell-sub">v{s.version}</small>
                        </>
                      )}
                    </td>
                    <td className="mono">
                      {s.listener ? `:${s.listener.port}` : "主端口"}
                      <button
                        className="table-link cell-sub"
                        title="查看同 IP 探索画板"
                        onClick={() =>
                          navigate("sessions", `ip:${encodeURIComponent(s.ip)}`)
                        }
                      >
                        {s.ip}
                      </button>
                    </td>
                    <td>{formatTime(s.created_at)}</td>
                    <td>
                      {s.delivery_count} / {s.report_count}
                    </td>
                    <td>
                      <Badge
                        tone={
                          s.report_count
                            ? "green"
                            : s.delivery_count
                              ? "purple"
                              : ""
                        }
                      >
                        {s.report_count
                          ? "已回传"
                          : s.delivery_count
                            ? "已投放"
                            : "已访问"}
                      </Badge>
                      {s.outcome !== "unknown" && (
                        <small className="cell-sub">
                          {outcomeLabels[s.outcome]}
                        </small>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <Empty
            title={loading ? "正在加载…" : "暂无会话记录"}
            description="使用测试入口开始实验，页面请求与回传会显示在这里。"
          />
        )}
        <div className="pagination">
          <span>共 {list.total} 条记录 · 每页 20 条</span>
          <div>
            <Button
              icon={ChevronLeft}
              aria-label="上一页"
              disabled={page <= 1}
              onClick={() => setPage(page - 1)}
            >
              上一页
            </Button>
            <span>
              {page} / {Math.max(1, Math.ceil(list.total / 20))}
            </span>
            <Button
              icon={ChevronRight}
              aria-label="下一页"
              disabled={page * 20 >= list.total}
              onClick={() => setPage(page + 1)}
            >
              下一页
            </Button>
          </div>
        </div>
      </Panel>
      <p className="page-note">
        时间按当前浏览器时区显示；数据库使用 UTC。历史记录使用创建时的配置快照。
      </p>
      {clearing && (
        <Confirm
          title="清除全部记录"
          description="这会删除所有会话、事件、回传与 IP 探索记录，包括筛选范围外的记录。页面模板、提示词及版本历史、部署配置会保留。此操作不可撤销。"
          onClose={() => setClearing(false)}
          loading={busy}
          onConfirm={async () => {
            setBusy(true);
            const r = await mutate(
              "/sessions",
              "DELETE",
              undefined,
              "全部实验记录已清除",
            );
            setBusy(false);
            if (r) {
              setClearing(false);
              setPage(1);
              load();
            }
          }}
        />
      )}
    </>
  );
}
