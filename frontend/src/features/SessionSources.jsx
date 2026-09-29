import React, { lazy, Suspense, useEffect, useState } from "react";
import {
  ArrowLeft,
  Network,
  RefreshCw,
  Search,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";
import { useApp } from "../App";
import { api, formatTime } from "../api";
import {
  Button,
  PageHeader,
  Panel,
  Field,
  Empty,
  Badge,
} from "../components/UI";
import Select from "../components/Select";
import FlowErrorBoundary from "./FlowErrorBoundary";
import "./session-sources.css";

const AccessFlowGraph = lazy(() =>
  import("./AccessFlow").then((module) => ({
    default: module.AccessFlowGraph,
  })),
);

export default function SessionSources({ ip }) {
  const { data, navigate } = useApp();
  const [search, setSearch] = useState("");
  const [deployment, setDeployment] = useState("");
  const [ua, setUA] = useState("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [page, setPage] = useState(1);
  const [before, setBefore] = useState(0);
  const [live, setLive] = useState(true);
  const [refresh, setRefresh] = useState(0);
  const [loaded, setLoaded] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const query = new URLSearchParams({ page: String(page), deployment });
  if (ip) query.set("ip", ip);
  else query.set("search", search);
  if (ua !== "all") query.set("ua", ua.slice(3));
  if (from) query.set("from", new Date(from).toISOString());
  if (to) query.set("to", new Date(to).toISOString());
  if (before) query.set("before", String(before));
  const url = `/session-sources${ip ? "/flow" : ""}?${query}`;
  const record = loaded?.url === url ? loaded.value : null;
  const change = (setter, value) => {
    setter(value);
    setPage(1);
    setBefore(0);
  };
  useEffect(() => {
    let active = true,
      timer;
    const load = async () => {
      setLoading(true);
      try {
        const value = await api(url);
        if (active) {
          setLoaded({ url, value });
          setError("");
        }
      } catch (e) {
        if (active) setError(e.message);
      } finally {
        if (active) {
          setLoading(false);
          if (live && page === 1) timer = setTimeout(load, 5000);
        }
      }
    };
    timer = setTimeout(load, 180);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [url, live, refresh, page]);
  const pages = Math.max(
    1,
    Math.ceil((record?.total || 0) / (record?.page_size || 20)),
  );
  return (
    <div className="session-sources">
      <button
        className="back-link"
        onClick={() => navigate("sessions", ip ? "ip" : undefined)}
      >
        <ArrowLeft size={16} />
        {ip ? "返回来源列表" : "返回会话列表"}
      </button>
      <PageHeader
        title={ip ? `${ip} · 探索画板` : "IP 探索画板"}
        description="汇总同一来源在各个蜜罐端口的访问、提示词交付、文件下载和回传。"
      >
        <Button
          icon={RefreshCw}
          loading={loading}
          onClick={() => setRefresh((v) => v + 1)}
        >
          刷新记录
        </Button>
      </PageHeader>
      <Panel>
        <div className="source-filters">
          {!ip && (
            <Field label="搜索来源">
              <div className="search-input">
                <Search size={16} />
                <input
                  aria-label="搜索来源 IP 或 User-Agent"
                  value={search}
                  onChange={(e) => change(setSearch, e.target.value)}
                  placeholder="IP 或 User-Agent…"
                />
              </div>
            </Field>
          )}
          <Field label="工作区 / 部署">
            <Select
              aria-label="探索工作区"
              value={deployment}
              onChange={(e) => change(setDeployment, e.target.value)}
            >
              <option value="">全部工作区与端口</option>
              {[
                ...(data.deployments || []),
                ...(data.legacy_deployments || []),
              ].map((d) => (
                <option key={d.id} value={d.id}>
                  {d.name}
                </option>
              ))}
            </Select>
          </Field>
          {ip && (
            <Field label="User-Agent">
              <Select
                aria-label="探索 User-Agent"
                value={ua}
                onChange={(e) => change(setUA, e.target.value)}
              >
                <option value="all">全部 User-Agent</option>
                {(record?.user_agents || loaded?.value?.user_agents || []).map(
                  (agent) => (
                    <option key={agent.ua} value={`ua:${agent.ua}`}>
                      {agent.ua || "未提供 User-Agent"} · {agent.record_count}{" "}
                      条
                    </option>
                  ),
                )}
              </Select>
            </Field>
          )}
          <Field label="开始时间">
            <input
              type="datetime-local"
              aria-label="探索开始时间"
              value={from}
              onChange={(e) => change(setFrom, e.target.value)}
            />
          </Field>
          <Field label="结束时间">
            <input
              type="datetime-local"
              aria-label="探索结束时间"
              value={to}
              onChange={(e) => change(setTo, e.target.value)}
            />
          </Field>
        </div>
        <div className="source-controls">
          <label>
            <input
              type="checkbox"
              checked={live}
              onChange={(e) => setLive(e.target.checked)}
            />
            自动刷新最新一页（5 秒）
          </label>
          <span className="muted">
            IP 和 User-Agent 用于关联观测，不能唯一识别 AI。
          </span>
        </div>
      </Panel>
      {error && (
        <p className="composer-error" role="alert">
          {error}
        </p>
      )}
      {!record ? (
        <Empty title={loading ? "正在加载访问记录…" : "暂无访问记录"} />
      ) : ip ? (
        <>
          <div className="source-board-summary">
            <strong>
              {record.total} 条记录 · 本页 {record.access_flow.groups.length}{" "}
              个会话分组
            </strong>
            <span className="muted">
              按来源与攻击阶段追踪分支；完整请求保留逐条时序。
            </span>
          </div>
          <FlowErrorBoundary key={ip}>
            <Suspense fallback={<Empty title="正在加载探索画板…" />}>
              <AccessFlowGraph
                key={url}
                flow={record.access_flow}
                storageKey={`source:${url}`}
                onSession={(id) => navigate("sessions", id)}
              />
            </Suspense>
          </FlowErrorBoundary>
        </>
      ) : (
        <Panel>
          {record.items.length ? (
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>来源 IP</th>
                    <th>会话 / User-Agent / 端口</th>
                    <th>访问记录</th>
                    <th>提示词交付 / 回传</th>
                    <th>最近访问</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {record.items.map((item) => (
                    <tr key={item.ip}>
                      <td>
                        <button
                          className="table-link mono"
                          onClick={() =>
                            navigate(
                              "sessions",
                              `ip:${encodeURIComponent(item.ip)}`,
                            )
                          }
                        >
                          {item.ip || "未知来源"}
                        </button>
                      </td>
                      <td>
                        {item.session_count} / {item.user_agent_count} /{" "}
                        {item.listener_count}
                      </td>
                      <td>{item.record_count}</td>
                      <td>
                        <Badge
                          tone={
                            item.report_count
                              ? "green"
                              : item.delivery_count
                                ? "purple"
                                : ""
                          }
                        >
                          {item.delivery_count} / {item.report_count}
                        </Badge>
                      </td>
                      <td>{formatTime(item.last_seen)}</td>
                      <td>
                        <Button
                          icon={Network}
                          onClick={() =>
                            navigate(
                              "sessions",
                              `ip:${encodeURIComponent(item.ip)}`,
                            )
                          }
                        >
                          查看画板
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <Empty
              title="暂无来源记录"
              description="访问蜜罐端口后，请求会自动记录在这里。"
            />
          )}
        </Panel>
      )}
      {record && (
        <div className="pagination source-pagination">
          <span>
            {ip
              ? `共 ${record.total} 条记录 · 每页最多 ${record.page_size} 条 · 第 1 页为最新记录`
              : `共 ${record.total} 个来源 IP`}
          </span>
          <div>
            <Button
              icon={ChevronLeft}
              disabled={page <= 1 || loading}
              onClick={() => {
                setPage((v) => v - 1);
                if (page === 2) setBefore(0);
              }}
            >
              {ip ? "较新记录" : "上一页"}
            </Button>
            <span>
              {page} / {pages}
            </span>
            <Button
              icon={ChevronRight}
              disabled={page >= pages || loading}
              onClick={() => {
                if (ip) setBefore(record.snapshot_id);
                setPage((v) => v + 1);
              }}
            >
              {ip ? "较早记录" : "下一页"}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
