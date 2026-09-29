import React, { useEffect, useState } from "react";
import {
  Plug,
  Plus,
  Save,
  RefreshCw,
  Trash2,
  ArrowUpRight,
} from "lucide-react";
import { api } from "../api";
import { useApp } from "../App";
import { Button, Field, PageHeader, Panel } from "../components/UI";
import Select from "../components/Select";
import "./agents.css";

const fresh = () => ({
  name: "",
  transport: "http",
  url: "",
  command: "",
  args: [],
  headers: {},
  env: {},
  enabled: true,
  timeout_seconds: 30,
  tools: [],
});
const edit = (server) => ({
  ...server,
  argsText: JSON.stringify(server.args || [], null, 2),
  headersText: JSON.stringify(server.headers || {}, null, 2),
  envText: JSON.stringify(server.env || {}, null, 2),
});
const statusText = (server) =>
  !server.enabled
    ? "已停用"
    : { ready: "连接正常", error: "连接失败", unchecked: "尚未测试" }[
        server.status
      ] || "尚未测试";

export default function MCPManagement() {
  const { notify, navigate } = useApp();
  const [servers, setServers] = useState([]),
    [agents, setAgents] = useState([]);
  const [draft, setDraft] = useState(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState("");
  useEffect(() => {
    let live = true;
    Promise.all([api("/mcp"), api("/agents")])
      .then(([m, a]) => {
        if (!live) return;
        setServers(m.items);
        setAgents(a.items);
        setDraft(edit(m.items[0] || fresh()));
      })
      .catch((e) => live && setError(e.message));
    return () => {
      live = false;
    };
  }, []);
  const patch = (values) => setDraft((d) => ({ ...d, ...values }));
  const select = (server) => {
    setDraft(edit(server));
    setError("");
  };
  const update = (server) => {
    setServers((items) => [...items.filter((s) => s.id !== server.id), server]);
    setDraft(edit(server));
  };
  const save = async (test) => {
    setBusy(test ? "test" : "save");
    setError("");
    try {
      const parse = (text, label) => {
        try {
          return JSON.parse(text);
        } catch {
          throw new Error(`${label}需要有效的 JSON`);
        }
      };
      const body = {
        ...draft,
        args: parse(draft.argsText, "命令参数"),
        headers: parse(draft.headersText, "请求头"),
        env: parse(draft.envText, "环境变量"),
      };
      delete body.argsText;
      delete body.headersText;
      delete body.envText;
      let saved = await api("/mcp", "POST", body);
      update(saved);
      if (test) {
        saved = await api(`/mcp/${saved.id}/test`, "POST", {
          version: saved.version,
        });
        update(saved);
        if (saved.status === "error") {
          setError(saved.error);
          return;
        }
      }
      notify(
        test ? `连接正常，发现 ${saved.tools.length} 个工具` : "MCP 配置已保存",
      );
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy("");
    }
  };
  const remove = async () => {
    if (!window.confirm(`删除“${draft.name}”及其 Agent 绑定？`)) return;
    setBusy("delete");
    setError("");
    try {
      await api(`/mcp/${draft.id}/delete`, "POST", { version: draft.version });
      const remaining = servers.filter((s) => s.id !== draft.id);
      setServers(remaining);
      select(remaining[0] || fresh());
      setAgents((await api("/agents")).items);
      notify("MCP 服务及绑定已删除");
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy("");
    }
  };
  return (
    <>
      <PageHeader
        title="MCP 服务"
        description="添加外部工具服务，在 Agent 管理中选择允许加载的服务。新任务使用保存后的配置。"
      >
        <Button icon={Plus} disabled={!!busy} onClick={() => select(fresh())}>
          添加 MCP
        </Button>
        <Button icon={ArrowUpRight} onClick={() => navigate("agents")}>
          配置 Agent 绑定
        </Button>
      </PageHeader>
      {error && (
        <div className="composer-error" role="alert">
          {error}
        </div>
      )}
      <div className="agent-management-layout mcp-management">
        <aside className="agent-definition-list" aria-label="MCP 服务列表">
          {servers.length === 0 && (
            <p className="muted">还没有 MCP 服务，填写连接信息即可添加。</p>
          )}
          {servers.map((server) => (
            <button
              key={server.id}
              disabled={!!busy}
              className={server.id === draft?.id ? "active" : ""}
              onClick={() => select(server)}
            >
              <Plug size={19} />
              <span>
                <strong>{server.name}</strong>
                <small>
                  {server.transport} · {statusText(server)}
                  <br />
                  {server.tools?.length || 0} 个工具 ·{" "}
                  {
                    agents.filter((a) => a.mcp_servers?.includes(server.id))
                      .length
                  }{" "}
                  个 Agent
                </small>
              </span>
            </button>
          ))}
        </aside>
        {draft && (
          <div className="composer-stack">
            <Panel title={draft.id ? draft.name : "添加 MCP 服务"}>
              <fieldset
                className="mcp-form padded composer-stack"
                disabled={!!busy}
              >
                <div className="form-grid">
                  <Field label="服务名称">
                    <input
                      value={draft.name}
                      onChange={(e) => patch({ name: e.target.value })}
                      placeholder="例如：文档查询"
                    />
                  </Field>
                  <Field label="连接方式">
                    <Select
                      value={draft.transport}
                      onChange={(e) => patch({ transport: e.target.value })}
                    >
                      <option value="http">Streamable HTTP</option>
                      <option value="sse">SSE（兼容旧服务）</option>
                      <option value="stdio">本地命令（stdio）</option>
                    </Select>
                  </Field>
                  <Field label="连接及调用超时（秒）">
                    <input
                      type="number"
                      min="1"
                      max="300"
                      value={draft.timeout_seconds}
                      onChange={(e) =>
                        patch({ timeout_seconds: Number(e.target.value) })
                      }
                    />
                  </Field>
                  <label className="agent-check">
                    <input
                      type="checkbox"
                      checked={draft.enabled}
                      onChange={(e) => patch({ enabled: e.target.checked })}
                    />
                    启用服务
                  </label>
                </div>
                {draft.transport === "stdio" ? (
                  <>
                    <Field
                      label="可执行命令"
                      hint="命令运行在 AgentMirror 服务所在机器。参数逐项填写，不经过 Shell。"
                    >
                      <input
                        value={draft.command}
                        placeholder="npx 或可执行文件的绝对路径"
                        onChange={(e) => patch({ command: e.target.value })}
                      />
                    </Field>
                    <Field label="命令参数（JSON 数组）">
                      <textarea
                        rows={3}
                        value={draft.argsText}
                        onChange={(e) => patch({ argsText: e.target.value })}
                        placeholder={'["-y", "your-mcp-package"]'}
                      />
                    </Field>
                    <Field
                      label="环境变量（JSON 对象）"
                      hint="已保存的值用 null 显示并保留；填写新值会替换，删除键会移除。"
                    >
                      <textarea
                        rows={4}
                        autoComplete="off"
                        spellCheck={false}
                        value={draft.envText}
                        onChange={(e) => patch({ envText: e.target.value })}
                      />
                    </Field>
                  </>
                ) : (
                  <>
                    <Field label="MCP 地址">
                      <input
                        type="url"
                        value={draft.url}
                        placeholder={
                          draft.transport === "sse"
                            ? "https://example.com/sse"
                            : "https://example.com/mcp"
                        }
                        onChange={(e) => patch({ url: e.target.value })}
                      />
                    </Field>
                    <Field
                      label="认证与自定义请求头（JSON 对象）"
                      hint={
                        '例如 {"Authorization": "Bearer …"}。已保存的值显示为 null；保留 null 不修改，删除键会移除。'
                      }
                    >
                      <textarea
                        rows={4}
                        autoComplete="off"
                        spellCheck={false}
                        value={draft.headersText}
                        onChange={(e) => patch({ headersText: e.target.value })}
                      />
                    </Field>
                  </>
                )}
                <div className="mcp-actions">
                  <Button
                    variant="primary"
                    icon={RefreshCw}
                    loading={busy === "test"}
                    onClick={() => save(true)}
                  >
                    保存并测试连接
                  </Button>
                  <Button
                    icon={Save}
                    loading={busy === "save"}
                    onClick={() => save(false)}
                  >
                    保存配置
                  </Button>
                  {draft.id && (
                    <Button
                      icon={Trash2}
                      loading={busy === "delete"}
                      onClick={remove}
                    >
                      删除服务
                    </Button>
                  )}
                </div>
              </fieldset>
            </Panel>
            <Panel
              title="可用工具"
              action={
                <span className="muted">
                  {statusText(draft)} · {draft.tools?.length || 0} 个
                </span>
              }
            >
              <div className="padded composer-stack">
                <p className="muted">
                  保存并测试连接后显示工具目录。只有已绑定的 Agent
                  才能加载并调用。
                </p>
                {draft.checked_at && (
                  <small className="muted">
                    最近检查：{new Date(draft.checked_at).toLocaleString()}
                  </small>
                )}
                {draft.error && (
                  <p role="status" className="composer-error">
                    {draft.error}
                  </p>
                )}
                <div className="agent-tool-list">
                  {draft.tools?.map((tool) => (
                    <div className="agent-tool" key={tool.name}>
                      <code>{tool.name}</code>
                      <p>{tool.description}</p>
                      <details>
                        <summary>参数定义</summary>
                        <pre>{JSON.stringify(tool.parameters, null, 2)}</pre>
                      </details>
                    </div>
                  ))}
                </div>
              </div>
            </Panel>
          </div>
        )}
      </div>
    </>
  );
}
