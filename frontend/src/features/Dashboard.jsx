import React, { useEffect, useState } from "react";
import {
  Layers,
  Users,
  FileText,
  Database,
  Plus,
  Link as LinkIcon,
  ExternalLink,
  ArrowRight,
  Inbox,
  Download,
  RefreshCw,
  Activity,
} from "lucide-react";
import { useApp } from "../App";
import { api, formatTime } from "../api";
import {
  DailyChart,
  trafficSeries,
  compactNumber,
  exactNumber,
} from "./OverviewCharts";
import "./overview.css";
import {
  Button,
  PageHeader,
  Panel,
  Empty,
  LinkButton,
  Badge,
} from "../components/UI";

export function WorkspaceTable({ items }) {
  const { data, navigate } = useApp();
  return (
    <div className="table-scroll">
      <table>
        <thead>
          <tr>
            <th>工作区</th>
            <th>场景实例</th>
            <th>发布版本</th>
            <th>状态</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {!items.length && (
            <tr className="empty-table-row">
              <td colSpan={5}>
                <Empty
                  icon={Inbox}
                  title="暂无蜜罐工作区"
                  description="组合站点素材、模拟场景和提示词，再预演发布。"
                >
                  <Button onClick={() => navigate("composer", "new")}>
                    创建工作区
                  </Button>
                </Empty>
              </td>
            </tr>
          )}
          {items.map((w) => {
            const deployment = data.deployments.find(
              (d) => d.id === w.deployment_id,
            );
            const ports = (data.listeners || []).filter(
              (l) =>
                l.deployment_id === deployment?.id && l.status === "running",
            );
            return (
              <tr key={w.id}>
                <td>
                  <span className="table-name">
                    <Layers size={18} />
                    <span>
                      {w.name}
                      <small className="cell-sub">{w.slug}</small>
                    </span>
                  </span>
                </td>
                <td>{w.bindings?.length || 0}</td>
                <td>
                  {deployment
                    ? `v${w.published_version || w.version} · 第 ${deployment.revision || 1} 次发布`
                    : "尚未发布"}
                </td>
                <td>
                  <Badge
                    tone={
                      !deployment
                        ? ""
                        : !deployment.enabled
                          ? "amber"
                          : ports.length
                            ? "green"
                            : ""
                    }
                  >
                    {!deployment
                      ? "草稿"
                      : !deployment.enabled
                        ? "已暂停"
                        : ports.length
                          ? `${ports.length} 个端口运行中`
                          : "待绑定端口"}
                  </Badge>
                </td>
                <td>
                  <LinkButton onClick={() => navigate("composer", w.id)}>
                    打开工作区
                  </LinkButton>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export function DefaultBinding() {
  const { data, navigate } = useApp();
  const listener = data.listeners?.find((l) => l.is_default);
  const deployment = data.deployments.find(
    (d) => d.id === listener?.deployment_id,
  );
  if (!listener || !deployment)
    return (
      <div className="default-binding">
        <span className="link-orb">
          <LinkIcon size={23} />
        </span>
        <div className="binding-copy">
          <strong>
            {listener?.deployment_id
              ? "默认端口需要绑定蜜罐工作区"
              : "尚未配置默认测试入口"}
          </strong>
          <small>
            {listener?.deployment_id
              ? "旧版编排已移除，请把测试端口绑定到已发布工作区。"
              : "发布工作区并绑定测试端口后，可在这里打开测试站点。"}
          </small>
        </div>
        <Button onClick={() => navigate("settings")}>测试端口管理</Button>
      </div>
    );
  return (
    <div className="default-binding">
      <span className="link-orb">
        <LinkIcon size={23} />
      </span>
      <div className="binding-copy">
        <strong>
          默认测试入口{" "}
          {(!deployment.enabled || listener.status !== "running") && (
            <Badge tone="amber">
              {listener.status === "error" ? "启动失败" : "未运行"}
            </Badge>
          )}
        </strong>
        <a href={`${listener.public_url}/`} target="_blank" rel="noreferrer">
          {listener.public_url}/
        </a>
        <small>
          {deployment.name} · 第 {deployment.revision || 1} 次发布 ·
          站点与场景固定快照
        </small>
      </div>
      <div className="binding-actions">
        <a
          className="button"
          href={`${listener.public_url}/`}
          target="_blank"
          rel="noreferrer"
        >
          <ExternalLink size={15} />
          打开测试页
        </a>
        <button
          className="text-button"
          onClick={() =>
            navigate("composer", deployment.workspace_id || deployment.id)
          }
        >
          打开工作区
          <ArrowRight size={15} />
        </button>
      </div>
    </div>
  );
}

function initialOverviewFilter() {
  const params = new URLSearchParams(location.search);
  const days = Number(params.get("overview_days"));
  return {
    days: [7, 30, 90].includes(days) ? days : 30,
  };
}

export default function Dashboard() {
  const { navigate } = useApp();
  const [filter, setFilter] = useState(initialOverviewFilter);
  const [snapshot, setSnapshot] = useState(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const url = new URL(location.href);
    url.searchParams.set("overview_days", filter.days);
    url.searchParams.delete("overview_model");
    history.replaceState(null, "", url);
    let alive = true,
      pending = false;
    const load = async () => {
      if (pending) return;
      pending = true;
      if (alive) setLoading(true);
      try {
        const params = new URLSearchParams({
          days: filter.days,
          offset: -new Date().getTimezoneOffset(),
        });
        const value = await api(`/overview?${params}`);
        if (alive) {
          setSnapshot(value);
          setError("");
        }
      } catch (e) {
        if (alive) setError(e.message);
      } finally {
        pending = false;
        if (alive) setLoading(false);
      }
    };
    load();
    const updateVisible = () => {
      if (!document.hidden) load();
    };
    const timer = setInterval(updateVisible, 30000);
    document.addEventListener("visibilitychange", updateVisible);
    window.addEventListener("online", updateVisible);
    return () => {
      alive = false;
      clearInterval(timer);
      document.removeEventListener("visibilitychange", updateVisible);
      window.removeEventListener("online", updateVisible);
    };
  }, [filter, revision]);
  const data = snapshot?.days === filter.days ? snapshot : null;
  const workspaces = snapshot?.workspaces;
  const summary = snapshot?.summary;
  const metrics = [
    [
      Layers,
      "蜜罐工作区",
      workspaces?.total,
      workspaces
        ? `已发布 ${workspaces.published} · 已暂停 ${workspaces.paused} · 草稿 ${workspaces.draft}`
        : "",
      "composer",
    ],
    [
      Users,
      "访问会话",
      summary?.sessions,
      summary ? `已投放 ${summary.delivered} 个会话` : "",
      "sessions",
    ],
    [
      FileText,
      "提示词交付",
      summary?.delivery_events,
      "累计交付请求",
      "sessions",
    ],
    [
      Download,
      "托管文件下载",
      summary?.downloads,
      "成功下载请求 · 含分段下载",
      "sessions",
    ],
    [Database, "收到回传", summary?.reports, "累计接收记录", "sessions"],
  ];
  const timezone = data
    ? `UTC${data.offset >= 0 ? "+" : "−"}${String(Math.floor(Math.abs(data.offset) / 60)).padStart(2, "0")}:${String(Math.abs(data.offset) % 60).padStart(2, "0")}`
    : "";
  return (
    <div className="overview-dashboard">
      <PageHeader
        title="总览"
        description={`系统全局状态 · 累计统计 · 每 30 秒刷新${snapshot ? ` · 更新于 ${new Date(snapshot.updated_at).toLocaleTimeString("zh-CN", { hour12: false })}` : ""}`}
      >
        <Button
          icon={RefreshCw}
          loading={loading}
          onClick={() => setRevision((v) => v + 1)}
        >
          刷新统计
        </Button>
        <Button
          icon={Plus}
          variant="primary"
          onClick={() => navigate("composer", "new")}
        >
          新建工作区
        </Button>
      </PageHeader>
      {error && (
        <div className="overview-error" role="alert">
          {data ? "刷新失败，当前显示上次统计。" : "统计加载失败："}
          {error}
          <Button
            onClick={() => {
              setRevision((v) => v + 1);
            }}
          >
            重试
          </Button>
        </div>
      )}
      <div className="overview-metrics">
        {metrics.map(([Icon, label, value, caption, route]) => (
          <button
            className="overview-metric"
            key={label}
            onClick={() => navigate(route)}
          >
            <span>
              <Icon size={17} />
              {label}
            </span>
            <strong>{value == null ? "—" : exactNumber(value)}</strong>
            <small>{caption || "正在加载统计"}</small>
          </button>
        ))}
      </div>
      <section
        className="overview-panel overview-activity-panel"
        aria-labelledby="overview-activity-title"
      >
        <div className="overview-panel-head">
          <h2 id="overview-activity-title">
            <Activity size={18} />
            蜜罐访问趋势
          </h2>
          <button className="text-button" onClick={() => navigate("sessions")}>
            查看访问记录 <ArrowRight size={15} />
          </button>
        </div>
        <div className="overview-range-row">
          <span>
            近 {filter.days} 天 · {timezone || "本地日期"} · 按访问日期统计
          </span>
          <div
            className="overview-range"
            role="group"
            aria-label="统计时间范围"
          >
            {[7, 30, 90].map((days) => (
              <button
                key={days}
                aria-pressed={days === filter.days}
                onClick={() => setFilter((v) => ({ ...v, days }))}
              >
                {days === 90 ? "3个月" : `${days}天`}
              </button>
            ))}
          </div>
        </div>
        {data ? (
          <>
            <div className="overview-activity-body">
              <div className="overview-activity-summary">
                <span>访问记录</span>
                <strong title={exactNumber(data.traffic.requests)}>
                  {compactNumber(data.traffic.requests)}
                </strong>
                <small>
                  {data.traffic.sources} 个来源 IP · 近 {data.days} 天
                </small>
                {trafficSeries
                  .filter((s) => s.key !== "requests")
                  .map((s) => (
                    <div className="overview-activity-breakdown" key={s.key}>
                      <div>
                        <span>{s.label}</span>
                        <b style={{ color: s.color }}>
                          {exactNumber(data.traffic[s.key])}
                        </b>
                      </div>
                      <div className="overview-meter">
                        <i
                          style={{
                            width: `${data.traffic.requests ? (data.traffic[s.key] / data.traffic.requests) * 100 : 0}%`,
                            background: s.color,
                          }}
                        />
                      </div>
                    </div>
                  ))}
              </div>
              <DailyChart
                key={`traffic-${filter.days}`}
                data={data.daily}
                series={trafficSeries}
                title="每日蜜罐访问"
              />
            </div>
            <p className="overview-note">
              交付、下载与回传分别计数；下载表示服务器已发送文件，不代表访问者已执行。历史记录按已保留的访问事件统计。
            </p>
          </>
        ) : (
          <div className="overview-loading" role="status">
            {error ? "暂无可显示的统计" : "正在加载蜜罐统计…"}
          </div>
        )}
      </section>
      {data && (
        <>
          <div className="overview-lower-grid">
            <section
              className="overview-panel"
              aria-labelledby="overview-response-title"
            >
              <div className="overview-panel-head">
                <h2 id="overview-response-title">
                  <Activity size={18} />
                  请求响应分布
                </h2>
                <span>近 {data.days} 天</span>
              </div>
              <div className="overview-status-bars overview-response-bars">
                {[
                  ["success", "成功 · 2xx", "var(--overview-green)"],
                  ["redirect", "重定向 · 3xx", "var(--overview-blue)"],
                  [
                    "client_error",
                    "请求被拒绝或无效 · 4xx",
                    "var(--overview-amber)",
                  ],
                  ["server_error", "服务异常 · 5xx", "#ef4444"],
                  ["unknown", "未记录状态码", "var(--muted)"],
                ].map(([key, label, color]) => (
                  <div key={key}>
                    <span>{label}</span>
                    <div className="overview-meter">
                      <i
                        style={{
                          width: `${data.traffic.requests ? (data.statuses[key] / data.traffic.requests) * 100 : 0}%`,
                          background: color,
                        }}
                      />
                    </div>
                    <b>{exactNumber(data.statuses[key])}</b>
                  </div>
                ))}
              </div>
              <p className="overview-note">
                按实际 HTTP 状态码归类，便于发现异常响应和无效探测。
              </p>
            </section>
            <section
              className="overview-panel"
              aria-labelledby="overview-status-title"
            >
              <div className="overview-panel-head">
                <h2 id="overview-status-title">
                  <Layers size={18} />
                  工作区状态
                </h2>
                <button
                  className="text-button"
                  onClick={() => navigate("composer")}
                >
                  查看全部
                  <ArrowRight size={15} />
                </button>
              </div>
              <div className="overview-status-total">
                <strong>{data.workspaces.total}</strong>
                <span>个工作区</span>
              </div>
              <div className="overview-status-bars">
                {[
                  ["published", "已发布", "var(--overview-green)"],
                  ["paused", "已暂停", "var(--overview-amber)"],
                  ["draft", "草稿", "var(--muted)"],
                ].map(([key, label, color]) => (
                  <div key={key}>
                    <span>{label}</span>
                    <div className="overview-meter">
                      <i
                        style={{
                          width: `${data.workspaces.total ? (data.workspaces[key] / data.workspaces.total) * 100 : 0}%`,
                          background: color,
                        }}
                      />
                    </div>
                    <b>{data.workspaces[key]}</b>
                  </div>
                ))}
              </div>
            </section>
          </div>
          <section className="overview-panel">
            <div className="overview-panel-head">
              <h2>最近会话</h2>
              <button
                className="text-button"
                onClick={() => navigate("sessions")}
              >
                查看全部
                <ArrowRight size={15} />
              </button>
            </div>
            {data.recent.length ? (
              <div className="table-scroll">
                <table className="overview-recent">
                  <thead>
                    <tr>
                      <th>工作区</th>
                      <th>时间</th>
                      <th>状态</th>
                      <th>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.recent.map((s) => (
                      <tr key={s.id}>
                        <td>
                          <strong>{s.deployment_name}</strong>
                          <small className="cell-sub mono">{s.id}</small>
                        </td>
                        <td>{formatTime(s.created_at)}</td>
                        <td>
                          <Badge tone={s.report_count ? "green" : ""}>
                            {s.report_count
                              ? "已回传"
                              : s.delivery_count
                                ? "已投放"
                                : "已访问"}
                          </Badge>
                        </td>
                        <td>
                          <LinkButton
                            onClick={() => navigate("sessions", s.id)}
                          >
                            查看会话
                          </LinkButton>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <Empty
                title="还没有访问会话"
                description="收到蜜罐访问后，这里会显示最近记录。"
              />
            )}
          </section>
        </>
      )}
    </div>
  );
}
