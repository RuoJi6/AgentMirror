import React, { useEffect, useState } from "react";
import { Plus, Trash2, Server, Zap } from "lucide-react";
import { useApp } from "../App";
import { api } from "../api";
import { Badge, Button, Field, Modal } from "../components/UI";
import Select from "../components/Select";

const presets = {
  custom: { name: "自定义服务", protocol: "openai", base_url: "", model: "" },
  openai: {
    name: "OpenAI",
    protocol: "openai",
    base_url: "https://api.openai.com/v1",
    model: "",
  },
  deepseek: {
    name: "DeepSeek",
    protocol: "openai",
    base_url: "https://api.deepseek.com/v1",
    model: "deepseek-chat",
  },
  qwen: {
    name: "通义千问",
    protocol: "openai",
    base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1",
    model: "qwen-plus",
  },
  moonshot: {
    name: "Moonshot",
    protocol: "openai",
    base_url: "https://api.moonshot.cn/v1",
    model: "",
  },
  ollama: {
    name: "Ollama",
    protocol: "openai",
    base_url: "http://127.0.0.1:11434/v1",
    model: "",
  },
  anthropic: {
    name: "Anthropic",
    protocol: "anthropic",
    base_url: "https://api.anthropic.com",
    model: "",
  },
};
const fresh = () => ({
  ...presets.custom,
  has_key: false,
  request_timeout_seconds: 180,
  context_window_tokens: 128000,
  max_output_tokens: 24000,
  job_timeout_seconds: 600,
  streaming: true,
});

export default function ModelProviders({ onClose, onSaved }) {
  const { notify } = useApp();
  const [items, setItems] = useState([]);
  const [defaultID, setDefaultID] = useState("");
  const [draft, setDraft] = useState(fresh);
  const [preset, setPreset] = useState("custom");
  const [key, setKey] = useState("");
  const [clearKey, setClearKey] = useState(false);
  const [makeDefault, setMakeDefault] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [deleting, setDeleting] = useState(false);

  const choose = (item, defaultValue = defaultID) => {
    setDraft(item || fresh());
    setPreset("custom");
    setKey("");
    setClearKey(false);
    setMakeDefault(!defaultValue || item?.id === defaultValue);
    setDeleting(false);
    setError("");
  };
  useEffect(() => {
    let stopped = false;
    api("/generation/providers")
      .then(({ items: next = [], default_provider_id = "" }) => {
        if (stopped) return;
        setItems(next);
        setDefaultID(default_provider_id);
        choose(
          next.find((item) => item.id === default_provider_id) || next[0],
          default_provider_id,
        );
        setLoaded(true);
      })
      .catch((e) => !stopped && setError(e.message));
    return () => {
      stopped = true;
    };
  }, []);

  const reload = async () => {
    const next = await api("/generation/providers");
    setItems(next.items || []);
    setDefaultID(next.default_provider_id || "");
    const current = next.items?.find(
      (item) => item.id === next.default_provider_id,
    );
    onSaved?.(current || { base_url: "", model: "", has_key: false });
    return next;
  };
  const save = async (test = false) => {
    setBusy(test ? "test" : "save");
    setError("");
    try {
      const saved = await api("/generation/providers", "POST", {
        ...(draft.id ? { id: draft.id } : {}),
        name: draft.name.trim(),
        protocol: draft.protocol,
        base_url: draft.base_url.trim(),
        model: draft.model.trim(),
        request_timeout_seconds: Number(draft.request_timeout_seconds),
        context_window_tokens: Number(draft.context_window_tokens ?? 128000),
        max_output_tokens: Number(draft.max_output_tokens ?? 24000),
        job_timeout_seconds: Number(draft.job_timeout_seconds),
        streaming: draft.streaming !== false,
        ...(key ? { api_key: key } : {}),
        ...(clearKey ? { clear_key: true } : {}),
        set_default: makeDefault,
      });
      const provider = saved.provider || saved;
      const next = await reload();
      choose(
        next.items?.find((item) => item.id === provider.id) || provider,
        next.default_provider_id,
      );
      if (test) {
        await api(`/generation/providers/${provider.id}/test`, "POST", {});
        notify("模型连接测试成功");
      } else notify("模型配置已保存");
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy("");
    }
  };
  const remove = async () => {
    setBusy("delete");
    setError("");
    try {
      await api(`/generation/providers/${draft.id}`, "DELETE");
      const next = await reload();
      choose(
        next.items?.find((item) => item.id === next.default_provider_id) ||
          next.items?.[0],
        next.default_provider_id,
      );
      notify("模型配置已删除");
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy("");
    }
  };
  const valid =
    loaded && draft.name.trim() && draft.base_url.trim() && draft.model.trim();
  return (
    <Modal
      title="生成模型设置"
      className="model-providers-modal"
      wide
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>关闭</Button>
          <Button
            icon={Zap}
            disabled={!valid || !!busy}
            loading={busy === "test"}
            onClick={() => save(true)}
          >
            保存并测试连接
          </Button>
          <Button
            variant="primary"
            disabled={!valid || !!busy}
            loading={busy === "save"}
            onClick={() => save(false)}
          >
            保存设置
          </Button>
        </>
      }
    >
      <div className="model-providers-layout">
        <aside className="model-providers-list" aria-label="模型配置列表">
          <Button
            icon={Plus}
            disabled={!loaded || !!busy}
            onClick={() => choose(null)}
          >
            添加模型配置
          </Button>
          {items.map((item) => (
            <button
              key={item.id}
              className={`model-provider-row ${draft.id === item.id ? "active" : ""}`}
              disabled={!!busy}
              onClick={() => choose(item)}
            >
              <Server size={17} />
              <span>
                <strong>{item.name}</strong>
                <small>{item.model}</small>
              </span>
              {item.id === defaultID && <Badge>默认</Badge>}
            </button>
          ))}
          {loaded && !items.length && (
            <p className="muted">保存第一个配置后，即可在对话中选择模型。</p>
          )}
        </aside>
        <fieldset
          className="model-provider-form"
          aria-label="模型配置编辑"
          disabled={!loaded || !!busy}
        >
          <div className="composer-section-line">
            <h3>{draft.id ? "编辑模型配置" : "新建模型配置"}</h3>
            {draft.has_key && <Badge tone="green">密钥已保存</Badge>}
          </div>
          <Field
            label="供应商预设"
            hint="选择预设填写服务地址，也可以使用自己的兼容接口。"
          >
            <Select
              value={preset}
              disabled={!!busy}
              onChange={(event) => {
                const value = event.target.value;
                setPreset(value);
                setDraft((current) => ({ ...current, ...presets[value] }));
              }}
            >
              {Object.entries(presets).map(([id, item]) => (
                <option key={id} value={id}>
                  {item.name}
                </option>
              ))}
            </Select>
          </Field>
          <div className="model-provider-fields">
            <Field label="配置名称">
              <input
                value={draft.name}
                onChange={(e) =>
                  setDraft((item) => ({ ...item, name: e.target.value }))
                }
                placeholder="例如：本地测试模型"
              />
            </Field>
            <Field label="接口协议">
              <Select
                value={draft.protocol || "openai"}
                onChange={(e) =>
                  setDraft((item) => ({ ...item, protocol: e.target.value }))
                }
              >
                <option value="openai">OpenAI Compatible</option>
                <option value="anthropic">Anthropic Messages</option>
              </Select>
            </Field>
          </div>
          <Field label="API Base URL">
            <input
              value={draft.base_url}
              onChange={(e) =>
                setDraft((item) => ({ ...item, base_url: e.target.value }))
              }
              placeholder="https://api.example.com/v1"
            />
          </Field>
          <Field label="模型名称">
            <input
              value={draft.model}
              onChange={(e) =>
                setDraft((item) => ({ ...item, model: e.target.value }))
              }
              placeholder="填写服务支持的模型 ID"
            />
          </Field>
          <div className="model-provider-fields">
            <Field
              label="单次模型请求超时（秒）"
              hint="10–3600 秒，包含等待回复和读取完整响应。"
            >
              <input
                type="number"
                min="10"
                max="3600"
                step="1"
                value={draft.request_timeout_seconds ?? 180}
                onChange={(e) =>
                  setDraft((item) => ({
                    ...item,
                    request_timeout_seconds: e.target.value,
                  }))
                }
              />
            </Field>
            <Field
              label="整个任务超时（秒）"
              hint="30–7200 秒，包含模型请求、重试和工具执行；不能小于单次超时。"
            >
              <input
                type="number"
                min="30"
                max="7200"
                step="1"
                value={draft.job_timeout_seconds ?? 600}
                onChange={(e) =>
                  setDraft((item) => ({
                    ...item,
                    job_timeout_seconds: e.target.value,
                  }))
                }
              />
            </Field>
          </div>
          <div className="model-provider-fields">
            <Field
              label="上下文窗口（token）"
              hint="填写模型支持的总窗口。接近输入预算时自动整理旧对话和工具记录，保留当前请求及最近调用。"
            >
              <input
                type="number"
                min="16000"
                max="1048576"
                step="1"
                value={draft.context_window_tokens ?? 128000}
                onChange={(e) =>
                  setDraft((item) => ({
                    ...item,
                    context_window_tokens: e.target.value,
                  }))
                }
              />
            </Field>
            <Field
              label="单次输出上限（token）"
              hint="包含服务计入的思考 token；按模型支持的额度设置。截断时会重试更小的完整步骤，不执行残缺工具参数。"
            >
              <input
                type="number"
                min="512"
                max="131072"
                step="1"
                value={draft.max_output_tokens ?? 24000}
                onChange={(e) =>
                  setDraft((item) => ({
                    ...item,
                    max_output_tokens: e.target.value,
                  }))
                }
              />
            </Field>
          </div>
          <p className="muted">
            保存后对新发起的任务生效，正在执行的任务沿用原设置。连接测试使用单次请求超时。
          </p>
          <Field
            label="响应方式"
            hint="流式返回会逐步显示模型输出的思考与回复；服务不支持流式时可关闭。"
          >
            <span className="check-row">
              <input
                type="checkbox"
                aria-label="流式返回"
                checked={draft.streaming !== false}
                onChange={(e) =>
                  setDraft((item) => ({ ...item, streaming: e.target.checked }))
                }
              />
              流式返回
            </span>
          </Field>
          <Field
            label="API Key"
            hint={
              draft.has_key
                ? "留空保留原密钥；填写新值替换。密钥不会回显。"
                : "无须鉴权的本地服务可以留空。"
            }
          >
            <input
              type="password"
              autoComplete="new-password"
              value={key}
              onChange={(e) => {
                setKey(e.target.value);
                if (e.target.value) setClearKey(false);
              }}
              placeholder={draft.has_key ? "已配置 · 留空保留" : "API Key"}
            />
          </Field>
          <div className="model-provider-options">
            <label>
              <input
                type="checkbox"
                checked={makeDefault}
                disabled={draft.id === defaultID}
                onChange={(e) => setMakeDefault(e.target.checked)}
              />
              设为新对话的默认模型
            </label>
            {draft.has_key && (
              <label>
                <input
                  type="checkbox"
                  checked={clearKey}
                  onChange={(e) => {
                    setClearKey(e.target.checked);
                    if (e.target.checked) setKey("");
                  }}
                />
                清除已保存密钥
              </label>
            )}
          </div>
          {draft.id && (
            <div className="model-provider-delete">
              {deleting ? (
                <>
                  <span>删除「{draft.name}」配置？历史对话会保留。</span>
                  <Button disabled={!!busy} onClick={() => setDeleting(false)}>
                    取消
                  </Button>
                  <Button
                    variant="danger"
                    loading={busy === "delete"}
                    onClick={remove}
                  >
                    确认删除
                  </Button>
                </>
              ) : (
                <Button
                  icon={Trash2}
                  variant="ghost danger-text"
                  disabled={!!busy}
                  onClick={() => setDeleting(true)}
                >
                  删除配置
                </Button>
              )}
            </div>
          )}
          {error && (
            <p role="alert" className="composer-error">
              {error}
            </p>
          )}
        </fieldset>
      </div>
    </Modal>
  );
}
