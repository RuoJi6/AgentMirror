import React, { useEffect, useRef, useState } from "react";
import { Eye, RotateCcw, Save } from "lucide-react";
import { useApp } from "../App";
import { api } from "../api";
import { Badge, Button, Field, Panel } from "../components/UI";
import Select from "../components/Select";
import HeaderFields from "./ComposerHeaders";
import "./collect-response.css";

const defaults = (rejected = false) => ({
  enabled: false,
  status: rejected ? 403 : 200,
  format: "json",
  content_type: "application/json; charset=utf-8",
  headers: {},
  body: rejected
    ? '{"error":"实验编号或令牌无效"}'
    : '{"ok":true,"message":"回传已接收","receipt_id":"{{receipt_id}}"}',
});
const formats = {
  json: "application/json; charset=utf-8",
  text: "text/plain; charset=utf-8",
  html: "text/html; charset=utf-8",
};

export default function CollectResponseSettings({
  rejected = false,
  workspace,
  onSaved,
}) {
  const { data, refresh, notify } = useApp();
  const configKey = rejected ? "collect_rejected_response" : "collect_response";
  const endpoint = rejected
    ? `/workspaces/${workspace.id}/collect-rejected-response`
    : "/collect-response";
  const title = rejected ? "令牌失效响应" : "回传成功响应";
  const stored = JSON.stringify(
    (rejected
      ? workspace.collect_rejected_response
      : data.settings[configKey]) || defaults(rejected),
  );
  const [draft, setDraft] = useState(() => JSON.parse(stored));
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [preview, setPreview] = useState(null);
  const editor = useRef(null);
  useEffect(() => {
    if (!dirty) {
      setDraft(JSON.parse(stored));
      setPreview(null);
    }
  }, [stored]);
  const patch = (value) => {
    setDraft((s) => ({ ...s, ...value }));
    setDirty(true);
    setPreview(null);
    setError("");
  };
  const insert = (name) => {
    const input = editor.current;
    const start = input?.selectionStart ?? draft.body.length;
    const end = input?.selectionEnd ?? start;
    const variable = `{{${name}}}`;
    patch({
      body: draft.body.slice(0, start) + variable + draft.body.slice(end),
    });
    requestAnimationFrame(() => {
      input?.focus();
      input?.setSelectionRange(
        start + variable.length,
        start + variable.length,
      );
    });
  };
  const run = async (action) => {
    setBusy(action);
    setError("");
    try {
      if (action === "preview")
        setPreview(await api(endpoint + "/preview", "POST", draft));
      else {
        const saved = await api(
          endpoint,
          "POST",
          workspace ? { ...draft, version: workspace.version } : draft,
        );
        const config = workspace ? saved.collect_rejected_response : saved;
        setDraft(config);
        setDirty(false);
        if (onSaved) await onSaved(saved);
        else await refresh();
        notify(config.enabled ? `${title}已生效` : `已恢复默认${title}`);
      }
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy("");
    }
  };
  return (
    <Panel title={title} className="collect-response-settings">
      <div className="padded">
        <p>
          {rejected
            ? "配置旧令牌或无效会话、令牌提交时收到的回复，默认 HTTP 403。仅改变响应，提交数据不会保存，令牌也不会恢复有效。"
            : "配置 Agent 成功提交回传后收到的回复。"}
          {rejected
            ? "仅作用于当前工作区绑定的蜜罐端口，保存后立即生效。"
            : "适用于所有蜜罐端口的回传入口，保存后立即生效。"}
        </p>
        <fieldset disabled={!!busy}>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={draft.enabled}
              onChange={(e) => patch({ enabled: e.target.checked })}
            />
            {rejected ? "启用自定义令牌失效响应" : "启用自定义回传响应"}
          </label>
          {!draft.enabled ? (
            <p className="collect-response-default">
              当前使用默认响应：HTTP {rejected ? 403 : 201} ·{" "}
              <code>
                {rejected
                  ? '{"error":"实验编号或令牌无效"}'
                  : '{"ok":true,"receipt_id":实际回传编号}'}
              </code>
            </p>
          ) : (
            <>
              <div className="form-grid collect-response-fields">
                <Field label="回传响应状态码">
                  <input
                    type="number"
                    min="200"
                    max="599"
                    value={draft.status}
                    onChange={(e) => patch({ status: Number(e.target.value) })}
                  />
                </Field>
                <Field label="回传响应格式">
                  <Select
                    value={draft.format}
                    onChange={(e) =>
                      patch({
                        format: e.target.value,
                        content_type: formats[e.target.value],
                      })
                    }
                  >
                    <option value="json">JSON</option>
                    <option value="text">纯文本</option>
                    <option value="html">HTML</option>
                  </Select>
                </Field>
                <Field label="回传响应 Content-Type">
                  <input
                    value={draft.content_type}
                    onChange={(e) => patch({ content_type: e.target.value })}
                  />
                </Field>
              </div>
              {!rejected && draft.status >= 300 && (
                <p className="muted">
                  此状态码可能让客户端跳转、认为提交失败或重试；已成功接收的回传数据仍会保存。
                </p>
              )}
              <Field
                label="回传响应正文"
                hint="JSON 必须为有效 JSON，变量放在字符串值中。204、205、304 状态码的正文须留空。"
              >
                <textarea
                  ref={editor}
                  className="collect-response-editor"
                  spellCheck="false"
                  value={draft.body}
                  onChange={(e) => patch({ body: e.target.value })}
                />
              </Field>
              <div className="actions">
                {!rejected && (
                  <Button onClick={() => insert("receipt_id")}>
                    插入回传编号
                  </Button>
                )}
                <Button onClick={() => insert("run_id")}>插入会话编号</Button>
              </div>
              <p className="muted">
                仅替换{" "}
                {!rejected && (
                  <>
                    <code>{"{{receipt_id}}"}</code> 和{" "}
                  </>
                )}
                <code>{"{{run_id}}"}</code>
                ；其他文字原样返回。预览使用示例编号，不创建回传记录。
              </p>
              <HeaderFields
                label="回传响应头"
                value={draft.headers}
                onChange={(headers) => patch({ headers })}
              />
            </>
          )}
        </fieldset>
        {error && (
          <p className="composer-error" role="alert">
            {error}
          </p>
        )}
        <div className="actions collect-response-actions">
          <Button
            icon={Eye}
            loading={busy === "preview"}
            disabled={!!busy}
            onClick={() => run("preview")}
          >
            预览回传响应
          </Button>
          <Button
            icon={RotateCcw}
            disabled={!!busy}
            onClick={() => patch(defaults(rejected))}
          >
            恢复默认配置
          </Button>
          <Button
            icon={Save}
            variant="primary"
            loading={busy === "save"}
            disabled={!!busy || !dirty}
            onClick={() => run("save")}
          >
            保存回传响应
          </Button>
          {dirty && <small className="muted">未保存</small>}
        </div>
        {preview && (
          <section
            className="collect-response-preview"
            aria-label="回传响应预览"
          >
            <div className="actions">
              <strong>响应预览</strong>
              <Badge>HTTP {preview.status}</Badge>
              <code>{preview.content_type}</code>
            </div>
            {Object.keys(preview.headers || {}).length > 0 && (
              <pre>
                {Object.entries(preview.headers)
                  .map(([k, v]) => `${k}: ${v}`)
                  .join("\n")}
              </pre>
            )}
            <pre aria-label="预览响应正文">{preview.body || "（无正文）"}</pre>
            <p className="muted">
              仅显示响应原文。JSON 字符串会正确转义，HTML 不在预览中执行。
            </p>
          </section>
        )}
      </div>
    </Panel>
  );
}
