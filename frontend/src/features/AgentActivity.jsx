import React, { lazy, Suspense, useEffect, useState } from "react";
import { ChevronRight, Loader2, Wrench, MessageSquare } from "lucide-react";
import { api, formatTime } from "../api";
import { formatContent } from "../components/TextContent";

const ChatMarkdown = lazy(() => import("../components/ChatMarkdown"));

const running = (turn) => ["queued", "running"].includes(turn.status);
const groupTools = (events) => {
  const groups = new Map();
  for (const event of events || []) {
    if (!event.call_id || !event.tool) continue;
    if (!groups.has(event.call_id))
      groups.set(event.call_id, { id: event.call_id });
    const group = groups.get(event.call_id);
    if (event.stage === "tool_call") group.input = event;
    if (["tool_result", "tool_error"].includes(event.stage))
      group.output = event;
  }
  return [...groups.values()];
};

function Payload({ value, label }) {
  return value ? (
    <>
      <pre className="agent-tool-json" aria-label={label} tabIndex={0}>
        {formatContent(value.text || "")}
      </pre>
      {value.truncated && (
        <small className="muted">
          内容较长，显示前 8,192 字符（共 {value.characters.toLocaleString()}{" "}
          字符）。
        </small>
      )}
    </>
  ) : (
    <p className="muted">未记录详细内容。</p>
  );
}

function ToolCall({ group, turn, readDetails, loaded, loadError, loading }) {
  const [open, setOpen] = useState(false);
  const result = group.output;
  const name = group.input?.tool || result?.tool;
  const status = result
    ? result.stage === "tool_error"
      ? "failed"
      : "completed"
    : running(turn)
      ? "running"
      : "interrupted";
  const labels = {
    completed: "成功",
    failed: "失败",
    running: "执行中",
    interrupted: "已中断",
  };
  const cached = loaded?.find((item) => item.id === group.id);
  const details = {
    input: {
      ...group.input,
      input: group.input?.input || cached?.input?.input,
    },
    output: { ...result, output: result?.output || cached?.output?.output },
  };
  const hasMissingPayload =
    (group.input?.has_payload && !details.input.input) ||
    (result?.has_payload && !details.output.output);
  useEffect(() => {
    if (open && hasMissingPayload && !loading && !loadError) readDetails();
  }, [open, hasMissingPayload, result?.id, loading, loadError]);
  return (
    <details
      className="agent-tool-call"
      data-status={status}
      onToggle={(event) => {
        setOpen(event.currentTarget.open);
      }}
    >
      <summary>
        <ChevronRight className="agent-tool-chevron" size={14} />
        {status === "running" ? (
          <Loader2 className="agent-tool-spinner" size={16} />
        ) : (
          <Wrench className="agent-tool-icon" size={16} />
        )}
        <span className="agent-tool-name">{name}</span>
        <span className="agent-tool-preview">
          {group.input?.input_preview || ""}
        </span>
        <span className="agent-tool-status">{labels[status]}</span>
      </summary>
      {open && (
        <div className="agent-tool-detail">
          <div className="agent-tool-meta">
            <time>{formatTime(group.input?.time || result?.time)}</time>
            {result?.duration_ms != null && (
              <span>{result.duration_ms} ms</span>
            )}
          </div>
          {hasMissingPayload ? (
            loadError ? (
              <div className="composer-error">
                {loadError}{" "}
                <button
                  type="button"
                  className="text-button"
                  onClick={readDetails}
                >
                  重试
                </button>
              </div>
            ) : (
              <p className="muted">
                {loading ? "正在加载调用详情…" : "加载调用详情…"}
              </p>
            )
          ) : (
            <>
              <strong>输入参数</strong>
              <Payload
                value={details.input?.input}
                label={`${name} 输入参数`}
              />
              <strong>
                输出
                {result
                  ? status === "completed"
                    ? " ✓"
                    : " · 失败"
                  : ` · ${labels[status]}`}
              </strong>
              {result ? (
                <Payload
                  value={details.output?.output}
                  label={`${name} 输出`}
                />
              ) : (
                <p className="muted">
                  {status === "running"
                    ? "工具正在执行，结果返回后自动更新。"
                    : "执行已结束，未记录到工具返回。"}
                </p>
              )}
            </>
          )}
        </div>
      )}
    </details>
  );
}

function Reasoning({ event, loaded, readDetails, loading, error }) {
  const [open, setOpen] = useState(false);
  const value =
    event.reasoning || loaded?.find((item) => item.id === event.id)?.reasoning;
  useEffect(() => {
    if (open && !value && !loading && !error) readDetails();
  }, [open, !!value, loading, error]);
  return (
    <details
      className="agent-tool-call agent-reasoning"
      onToggle={(e) => {
        setOpen(e.currentTarget.open);
      }}
    >
      <summary>
        <ChevronRight className="agent-tool-chevron" size={14} />
        <MessageSquare size={16} />
        <span className="agent-tool-name">思考</span>
        <span className="agent-tool-preview">{event.reasoning_preview}</span>
        <span className="agent-tool-status">
          第 {event.round} 轮
          {event.attempt > 1 ? ` · 重试 ${event.attempt - 1}` : ""}
        </span>
      </summary>
      {open && (
        <div className="agent-tool-detail">
          <div className="agent-tool-meta">
            <time>{formatTime(event.time)}</time>
            <span>模型返回的思考内容</span>
          </div>
          {value ? (
            <>
              <Suspense fallback={<p className="muted">正在加载内容…</p>}>
                <ChatMarkdown text={value.text || ""} />
              </Suspense>
              {value.truncated && (
                <small className="muted">
                  内容较长，显示前 32,768 字符（共{" "}
                  {value.characters.toLocaleString()} 字符）。
                </small>
              )}
            </>
          ) : error ? (
            <p role="alert" className="composer-error">
              {error}{" "}
              <button
                type="button"
                className="text-button"
                onClick={readDetails}
              >
                重试
              </button>
            </p>
          ) : (
            <p className="muted">
              {loading ? "正在加载思考内容…" : "加载思考内容…"}
            </p>
          )}
        </div>
      )}
    </details>
  );
}

export default function AgentActivity({ turn }) {
  const [loaded, setLoaded] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const groups = groupTools(turn.events);
  const readDetails = async () => {
    if (loading) return;
    setLoading(true);
    setError("");
    try {
      setLoaded((await api(`/generation/jobs/${turn.id}`)).events);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  };
  const cachedTools = loaded ? groupTools(loaded) : null;
  const byCall = new Map(groups.map((group) => [group.id, group]));
  const seen = new Set();
  const activity = (turn.events || []).filter((event) => {
    if (["reasoning", "context_compacted", "mcp"].includes(event.stage))
      return true;
    if (!byCall.has(event.call_id) || seen.has(event.call_id)) return false;
    seen.add(event.call_id);
    return true;
  });
  const last = turn.events?.at(-1);
  const live = running(turn) ? turn.live_response : null;
  const responseIssues = (turn.events || []).filter((event) =>
    ["model_retry", "model_error"].includes(event.stage),
  );
  return (
    <>
      {!!activity.length && (
        <div className="agent-tools" aria-label="思考与工具调用">
          {activity.map((event) =>
            event.stage === "mcp" ? (
              <div key={event.id} className="agent-tool-meta" role="status">
                {event.detail}
              </div>
            ) : event.stage === "context_compacted" ? (
              <div key={event.id} className="agent-tool-meta" role="status">
                上下文已压缩 · 估算{" "}
                {event.before_estimated_tokens?.toLocaleString()} →{" "}
                {event.after_estimated_tokens?.toLocaleString()} token ·
                原始记录保留
              </div>
            ) : event.stage === "reasoning" ? (
              <Reasoning
                key={`reasoning-${event.id}`}
                event={event}
                loaded={loaded}
                readDetails={readDetails}
                error={error}
                loading={loading}
              />
            ) : (
              <ToolCall
                key={event.call_id}
                group={byCall.get(event.call_id)}
                turn={turn}
                readDetails={readDetails}
                loaded={cachedTools}
                loadError={error}
                loading={loading}
              />
            ),
          )}
        </div>
      )}
      {live && (
        <div className="agent-live-response" aria-label="模型实时响应">
          {!!live.reasoning && (
            <details className="agent-tool-call agent-reasoning">
              <summary>
                <ChevronRight className="agent-tool-chevron" size={14} />
                <MessageSquare size={16} />
                <span className="agent-tool-name">正在思考</span>
                <span className="agent-tool-preview">
                  {live.reasoning.slice(0, 140)}
                </span>
              </summary>
              <div className="agent-tool-detail">
                <Suspense fallback={<p className="muted">正在加载内容…</p>}>
                  <ChatMarkdown text={live.reasoning} />
                </Suspense>
              </div>
            </details>
          )}
          {!!live.text && (
            <Suspense fallback={<p className="muted">正在加载内容…</p>}>
              <ChatMarkdown text={live.text} />
            </Suspense>
          )}
          <div className="agent-model-wait" role="status">
            <Loader2 size={14} className="agent-tool-spinner" />
            <span>
              {live.tools?.length
                ? `正在生成工具参数：${live.tools.join("、")}`
                : live.text
                  ? "正在回复…"
                  : "正在接收模型思考…"}
              {live.first_content_ms != null &&
                ` · 首段 ${(live.first_content_ms / 1000).toFixed(1)} 秒`}
            </span>
          </div>
        </div>
      )}
      {running(turn) && !live && !groups.some((group) => !group.output) && (
        <div className="agent-model-wait" role="status">
          <Loader2 size={14} className="agent-tool-spinner" />
          <span>
            {last?.stage === "model"
              ? last.attempt > 1
                ? "正在重新请求模型…"
                : `等待模型回复${last.request_timeout_seconds ? `（单次最多 ${last.request_timeout_seconds} 秒）` : ""}…`
              : last?.detail || "正在准备请求…"}
          </span>
        </div>
      )}
      {!!responseIssues.length && (
        <details className="composer-agent-activity agent-response-diagnostics">
          <summary>
            模型响应诊断 · {responseIssues.length} 条记录
            {turn.status === "completed" ? " · 已恢复" : ""}
          </summary>
          <ol>
            {responseIssues.map((event) => (
              <li key={event.id}>
                <span>
                  {event.detail}
                  <small>
                    第 {event.round} 轮 · 第 {event.attempt} 次请求
                    {event.diagnostic?.elapsed_ms != null &&
                      ` · 已等待 ${(event.diagnostic.elapsed_ms / 1000).toFixed(1)} 秒`}
                    {event.diagnostic?.code && ` · ${event.diagnostic.code}`}
                    {event.diagnostic?.finish_reason &&
                      ` · 结束原因：${event.diagnostic.finish_reason}`}
                    {(event.diagnostic?.completion_tokens ??
                      event.diagnostic?.output_tokens) != null &&
                      ` · 输出 token：${event.diagnostic.completion_tokens ?? event.diagnostic.output_tokens}`}
                  </small>
                </span>
                <small>{formatTime(event.time)}</small>
              </li>
            ))}
          </ol>
        </details>
      )}
      {!running(turn) &&
        !groups.length &&
        !!turn.events?.length &&
        turn.result?.kind !== "message" && (
          <details className="composer-agent-activity">
            <summary>
              {turn.mode === "clone" ? "查看采集过程" : "查看历史进度"} ·{" "}
              {turn.events.length} 条活动
            </summary>
            <ol>
              {turn.events.map((event, index) => (
                <li key={event.id || index}>
                  <span>{event.detail}</span>
                  <small>{formatTime(event.time)}</small>
                </li>
              ))}
            </ol>
          </details>
        )}
    </>
  );
}
