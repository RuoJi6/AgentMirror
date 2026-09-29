import React, { useEffect, useState } from "react";
import { Play, Plus, Save, Trash2 } from "lucide-react";
import { api } from "../api";
import { useApp } from "../App";
import { Button, Field, Panel } from "../components/UI";
import Select from "../components/Select";
import "./agents.css";

const actions = {
  goto: "打开页面",
  fill: "填写输入框",
  click: "点击",
  select: "选择选项",
  assert_text: "验证可见文字",
  assert_visible: "验证元素可见",
};
export default function WorkspaceReview({ workspace, dirty, onStarted }) {
  const { notify, navigate } = useApp();
  const [policy, setPolicy] = useState(null),
    [agents, setAgents] = useState([]),
    [tools, setTools] = useState([]),
    [jobs, setJobs] = useState([]);
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(""),
    [triggerError, setTriggerError] = useState("");
  const base = `/workspace-reviews/${workspace.id}`;
  useEffect(() => {
    let live = true;
    Promise.all([
      api(base + "/policy"),
      api("/agents"),
      api(base),
      api("/agent-tools"),
    ])
      .then(([p, a, j, t]) => {
        if (live) {
          setPolicy(p);
          setAgents(a.items);
          setTools(t.items.filter((tool) => tool.role === "writer"));
          setJobs(j.items);
        }
      })
      .catch((e) => live && setError(e.message));
    return () => {
      live = false;
    };
  }, [base]);
  useEffect(() => {
    let live = true,
      timer;
    const poll = async () => {
      try {
        const [list, latestPolicy] = await Promise.all([
          api(base),
          api(base + "/policy"),
        ]);
        if (!live) return;
        setJobs(list.items);
        setTriggerError(latestPolicy.trigger_error || "");
      } catch (e) {
        if (live) setError(e.message);
      }
      if (live) timer = setTimeout(poll, 2500);
    };
    poll();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [base]);
  const act = async (key, task) => {
    setBusy(key);
    setError("");
    try {
      await task();
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy("");
    }
  };
  const save = async () => {
    const saved = await api(base + "/policy", "POST", policy);
    setPolicy(saved);
    notify("核查触发设置已保存");
  };
  const patchStep = (index, changes) =>
    setPolicy((p) => ({
      ...p,
      browser_steps: p.browser_steps.map((s, i) =>
        i === index ? { ...s, ...changes } : s,
      ),
    }));
  return (
    <div className="composer-stack workspace-review">
      <Panel
        title="内容核查"
        action={
          <div className="actions">
            <Button onClick={() => navigate("agents")}>
              管理 Agent 与工具
            </Button>
            <Button
              icon={Play}
              variant="primary"
              disabled={
                !policy ||
                !!busy ||
                dirty ||
                jobs.some((j) => j.status === "running")
              }
              loading={busy === "run"}
              onClick={() =>
                act("run", async () => {
                  await save();
                  const job = await api(base, "POST", {
                    agent_id: policy.agent_id,
                  });
                  onStarted?.(job);
                })
              }
            >
              开始完整核查
            </Button>
          </div>
        }
      >
        <div className="padded composer-stack">
          <p className="muted">
            基于已保存工作区，在临时环境中检查页面、登录后访问、接口条件、提示词响应和回传。结束后集中报告异常与未确认项。
          </p>
          {dirty && <p role="alert">请先保存工作区，再核查最新配置。</p>}
          {error && (
            <div className="composer-error" role="alert">
              {error}
            </div>
          )}
          {triggerError && (
            <p className="composer-error" role="alert">
              自动核查等待处理：{triggerError}
            </p>
          )}
          {policy && (
            <>
              <Field label="核查 Agent">
                <Select
                  value={policy.agent_id}
                  onChange={(e) =>
                    setPolicy({ ...policy, agent_id: e.target.value })
                  }
                >
                  {agents
                    .filter((a) => a.role === "reviewer")
                    .map((a) => (
                      <option key={a.id} value={a.id} disabled={!a.enabled}>
                        {a.name}
                        {a.enabled ? "" : "（已停用）"}
                      </option>
                    ))}
                </Select>
              </Field>
              <div className="actions">
                <label className="agent-check">
                  <input
                    type="checkbox"
                    checked={policy.after_save}
                    onChange={(e) =>
                      setPolicy({ ...policy, after_save: e.target.checked })
                    }
                  />
                  工作区或绑定素材保存后
                </label>
                <label className="agent-check">
                  <input
                    type="checkbox"
                    checked={policy.after_writer}
                    onChange={(e) =>
                      setPolicy({ ...policy, after_writer: e.target.checked })
                    }
                  />
                  编写 Agent 完成后
                </label>
                <Button
                  icon={Save}
                  loading={busy === "policy"}
                  onClick={() => act("policy", save)}
                >
                  保存触发设置
                </Button>
              </div>
              <details className="review-flow">
                <summary>更多触发条件与触发消息</summary>
                <div className="composer-stack padded">
                  <label className="agent-check">
                    <input
                      type="checkbox"
                      checked={policy.after_failure || false}
                      onChange={(e) =>
                        setPolicy({
                          ...policy,
                          after_failure: e.target.checked,
                        })
                      }
                    />
                    编写失败或超时后
                  </label>
                  <Field label="定时间隔（秒，0 为关闭）">
                    <input
                      type="number"
                      min="0"
                      max="86400"
                      value={policy.interval_seconds || 0}
                      onChange={(e) =>
                        setPolicy({
                          ...policy,
                          interval_seconds: Number(e.target.value),
                        })
                      }
                    />
                  </Field>
                  <label className="agent-check">
                    <input
                      type="checkbox"
                      checked={policy.on_tool || false}
                      onChange={(e) =>
                        setPolicy({ ...policy, on_tool: e.target.checked })
                      }
                    />
                    指定编写工具调用成功后
                  </label>
                  {policy.on_tool && (
                    <Field label="触发工具（可多选）">
                      <select
                        multiple
                        size="6"
                        value={policy.tool_names || []}
                        onChange={(e) =>
                          setPolicy({
                            ...policy,
                            tool_names: Array.from(
                              e.target.selectedOptions,
                              (o) => o.value,
                            ),
                          })
                        }
                      >
                        {tools.map((t) => (
                          <option key={t.name} value={t.name}>
                            {t.name}
                          </option>
                        ))}
                      </select>
                    </Field>
                  )}
                  <p className="muted">
                    自动触发会在 AI 助手内启动此 Agent
                    的会话，可继续追问。相同工作区的触发串行合并，核查自身的工具调用不会重复触发。
                  </p>
                  {Object.entries({
                    after_save: "保存后",
                    after_writer: "编写完成后",
                    after_failure: "编写失败后",
                    on_tool: "工具调用后",
                    interval: "定时触发",
                  }).map(([key, label]) => (
                    <Field key={key} label={`${label}发送的消息`}>
                      <textarea
                        rows="2"
                        value={policy.messages?.[key] || ""}
                        placeholder="留空使用默认的完整核查说明"
                        onChange={(e) =>
                          setPolicy({
                            ...policy,
                            messages: {
                              ...policy.messages,
                              [key]: e.target.value,
                            },
                          })
                        }
                      />
                    </Field>
                  ))}
                </div>
              </details>
              <details className="review-flow">
                <summary>
                  登录与访问步骤 · {policy.browser_steps.length} 步（可选）
                </summary>
                <p className="muted">
                  可指定登录输入、点击和内容断言；留空时由绑定模型读取站点并规划。每个流程使用独立浏览器，会话和
                  Cookie 在步骤间保留。
                </p>
                {policy.browser_steps.map((step, index) => (
                  <div className="review-step" key={index}>
                    <span>{index + 1}</span>
                    <Select
                      aria-label={`步骤 ${index + 1} 操作`}
                      value={step.action}
                      onChange={(e) =>
                        patchStep(index, { action: e.target.value })
                      }
                    >
                      {Object.entries(actions).map(([key, label]) => (
                        <option key={key} value={key}>
                          {label}
                        </option>
                      ))}
                    </Select>
                    <input
                      aria-label={`步骤 ${index + 1} 选择器`}
                      placeholder="CSS 选择器，例如 #username"
                      value={step.selector || ""}
                      onChange={(e) =>
                        patchStep(index, { selector: e.target.value })
                      }
                    />
                    <input
                      aria-label={`步骤 ${index + 1} 值`}
                      placeholder={
                        step.action === "goto"
                          ? "页面路径，例如 /login"
                          : "输入值或预期文字"
                      }
                      value={step.value || ""}
                      onChange={(e) =>
                        patchStep(index, { value: e.target.value })
                      }
                    />
                    <Button
                      icon={Trash2}
                      aria-label={`删除步骤 ${index + 1}`}
                      onClick={() =>
                        setPolicy({
                          ...policy,
                          browser_steps: policy.browser_steps.filter(
                            (_, i) => i !== index,
                          ),
                        })
                      }
                    />
                  </div>
                ))}
                <Button
                  icon={Plus}
                  disabled={policy.browser_steps.length >= 40}
                  onClick={() =>
                    setPolicy({
                      ...policy,
                      browser_steps: [
                        ...policy.browser_steps,
                        {
                          action: policy.browser_steps.length
                            ? "click"
                            : "goto",
                          selector: "",
                          value: policy.browser_steps.length ? "" : "/",
                        },
                      ],
                    })
                  }
                >
                  添加步骤
                </Button>
              </details>
            </>
          )}
        </div>
      </Panel>
    </div>
  );
}
