import React, { lazy, Suspense, useEffect, useState } from "react";
import { api } from "../api";
import Select from "../components/Select";
import { Button, Modal } from "../components/UI";
import "./agents.css";
import FlowErrorBoundary from "./FlowErrorBoundary";
const AccessFlowGraph = lazy(() =>
  import("./AccessFlow").then((m) => ({ default: m.AccessFlowGraph })),
);
const labels = {
  pass: "通过",
  fail: "异常",
  incomplete: "未确认",
  passed: "核查通过",
  issues: "发现异常",
};
export default function ReviewReport({ jobId, report }) {
  const [open, setOpen] = useState(false),
    [boardOpen, setBoardOpen] = useState(false),
    [full, setFull] = useState(null),
    [error, setError] = useState(""),
    [filter, setFilter] = useState("all");
  const value = report.checks ? report : full || report;
  useEffect(() => {
    if (!open || report.checks) return;
    let live = true;
    api(`/generation/jobs/${jobId}`)
      .then((j) => live && setFull(j.result))
      .catch((e) => live && setError(e.message));
    return () => {
      live = false;
    };
  }, [open, jobId, report.checks]);
  return (
    <details
      className="review-chat-report"
      onToggle={(e) => setOpen(e.currentTarget.open)}
    >
      <summary>
        {value.review_type === "redteam"
          ? value.redteam?.mode === "behavioral"
            ? `红队行为报告 · ${value.redteam.outcome_label || "正在读取"}`
            : "受控复现报告"
          : labels[value.verdict] || "核查报告"}{" "}
        {value.review_type !== "redteam" && (
          <>
            · 通过 {value.counts?.pass || 0} · 异常 {value.counts?.fail || 0} ·
            未确认 {value.counts?.incomplete || 0}
          </>
        )}
      </summary>
      {error && <p role="alert">{error}</p>}
      {open && (
        <div className="composer-stack">
          {value.redteam && (
            <div className="review-check" aria-label="红队演练证据">
              <strong>
                {value.redteam.mode === "behavioral"
                  ? value.redteam.outcome_label
                  : "受控红队演练"}
              </strong>
              {value.redteam.mode === "behavioral" && (
                <>
                  <p>独立任务：{value.redteam.task}</p>
                  <p>
                    模型：{value.redteam.provider?.model || "本轮所选模型"} ·
                    Agent 配置 v{value.redteam.agent_version}
                  </p>
                  <p>
                    报告发现 {value.redteam.detections} 次 · 报告拒绝{" "}
                    {value.redteam.refusals} 次 · 工具阻止{" "}
                    {value.redteam.blocked_actions} 次 · 输出匹配回传{" "}
                    {value.redteam.output_matched_callbacks} 次
                  </p>
                </>
              )}
              <p>
                访问 {value.redteam.requests} 次 · 提示词交付{" "}
                {value.redteam.deliveries} 次 · 原文命令执行{" "}
                {value.redteam.controlled_actions} 次 · 回传接收{" "}
                {value.redteam.callbacks_accepted} 次 · 诊断闭环{" "}
                {value.redteam.verified_canary_callbacks} 次
              </p>
              {value.redteam.mode === "behavioral" && (
                <>
                  <p>
                    关联页面的已执行操作 {value.redteam.attributed_actions || 0}{" "}
                    次 · 来源未确认的操作{" "}
                    {value.redteam.unattributed_actions || 0} 次
                  </p>
                  <details>
                    <summary>本轮角色说明与工具</summary>
                    <p>
                      {value.redteam.custom_instructions ||
                        "未添加自定义角色说明"}
                    </p>
                    <p>{(value.redteam.tools || []).join("、")}</p>
                  </details>
                </>
              )}
              <p className="muted">{value.redteam.notice}</p>
            </div>
          )}
          {value.redteam?.decisions?.map((d) => (
            <details key={d.id} className="review-check">
              <summary>
                {
                  {
                    suspected_injection: "发现疑似注入",
                    refused_instruction: "拒绝页面指令",
                    followed_instruction: "选择执行",
                    benign_content: "视为普通内容",
                    uncertain: "尚不能判断",
                  }[d.decision]
                }{" "}
                · {d.source_observation_id}
              </summary>
              <blockquote>{d.quote}</blockquote>
              <p>{d.reason}</p>
              <p className="muted">
                模型报告的判断，引用已与实际读取的响应核对。
              </p>
            </details>
          ))}
          {value.access_flow && (
            <Button onClick={() => setBoardOpen(true)}>查看访问流程画板</Button>
          )}
          {boardOpen && (
            <Modal
              title="访问流程画板"
              wide
              onClose={() => setBoardOpen(false)}
            >
              <FlowErrorBoundary key={jobId} onBack={() => setBoardOpen(false)}>
                <Suspense fallback={<p>正在加载画板…</p>}>
                  <AccessFlowGraph
                    flow={value.access_flow}
                    storageKey={jobId}
                  />
                </Suspense>
              </FlowErrorBoundary>
            </Modal>
          )}
          <Select
            aria-label="筛选聊天核查结果"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          >
            <option value="all">全部检查</option>
            <option value="fail">只看异常</option>
            <option value="incomplete">只看未确认</option>
            <option value="pass">只看通过</option>
          </Select>
          {!value.checks && !error && <p>正在读取报告…</p>}
          {(value.checks || [])
            .filter((c) => filter === "all" || c.status === filter)
            .map((c) => (
              <details key={c.id} className={`review-check ${c.status}`}>
                <summary>
                  {labels[c.status]} · {c.name}
                </summary>
                <p>{c.detail}</p>
                {c.evidence && <pre>{JSON.stringify(c.evidence, null, 2)}</pre>}
              </details>
            ))}
        </div>
      )}
    </details>
  );
}
