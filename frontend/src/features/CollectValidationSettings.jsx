import React, { useEffect, useState } from "react";
import { Plus, RotateCcw, Save, Trash2 } from "lucide-react";
import { useApp } from "../App";
import { api } from "../api";
import { Button, Field, Panel } from "../components/UI";
import Select from "../components/Select";
import "./collect-validation.css";

const emptyConfig = { enabled: false, fields: [] };
const fieldNamePattern = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;
const minimumLength = (type) =>
  type === "datetime" ? 20 : type === "ip" ? 2 : 1;
const diagnosticFields = [
  { name: "host", type: "string", required: true, max_length: 128 },
  { name: "user", type: "string", required: true, max_length: 128 },
  { name: "ip", type: "ip", required: true, max_length: 64 },
  { name: "ts", type: "datetime", required: true, max_length: 64 },
];
let nextFieldKey = 0;
const editableField = (field) => ({ ...field, key: ++nextFieldKey });
const editableConfig = (config) => ({
  enabled: !!config.enabled,
  fields: (config.fields || []).map(editableField),
});
const configPayload = (draft) => ({
  enabled: draft.enabled,
  fields: draft.fields.map(({ name, type, required, max_length }) => ({
    name,
    type,
    required,
    max_length: Number(max_length),
  })),
});
function validateConfig(draft) {
  if (draft.enabled && !draft.fields.length)
    return "启用校验时至少需要一个字段。";
  const names = new Set();
  for (const [index, field] of draft.fields.entries()) {
    const label = `字段 ${index + 1}`;
    if (!fieldNamePattern.test(field.name))
      return `${label}名称须以字母或下划线开头，仅含字母、数字和下划线，最多 64 个字符。`;
    if (names.has(field.name)) return `${label}名称与其他字段重复。`;
    names.add(field.name);
    const length = Number(field.max_length);
    if (
      !Number.isInteger(length) ||
      length < minimumLength(field.type) ||
      length > 4096
    )
      return `${label}最大长度须为 ${minimumLength(field.type)}–4096 的整数。`;
  }
  return "";
}

export default function CollectValidationSettings({ workspace, onSaved }) {
  // A workspace switch starts a separate editor; ordinary refreshes retain edits.
  return (
    <ValidationEditor
      key={workspace.id}
      workspace={workspace}
      onSaved={onSaved}
    />
  );
}

function ValidationEditor({ workspace, onSaved }) {
  const { refresh, notify } = useApp();
  const stored = JSON.stringify(workspace.collect_validation || emptyConfig);
  const version = workspace.version;
  const [editor, setEditor] = useState(() => ({
    draft: editableConfig(JSON.parse(stored)),
    version,
    dirty: false,
  }));
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    setEditor((current) =>
      current.dirty
        ? current
        : {
            draft: editableConfig(JSON.parse(stored)),
            version,
            dirty: false,
          },
    );
  }, [stored, version]);

  const { draft, dirty } = editor;
  const stale = dirty && editor.version !== version;
  const validationError = dirty ? validateConfig(draft) : "";
  const patch = (update) => {
    setEditor((current) => ({
      ...current,
      draft: update(current.draft),
      dirty: true,
    }));
    setError("");
  };
  const patchField = (key, changes) =>
    patch((current) => ({
      ...current,
      fields: current.fields.map((field) =>
        field.key === key ? { ...field, ...changes } : field,
      ),
    }));
  const reloadSaved = async () => {
    setBusy("reload");
    setError("");
    try {
      const saved = await api(`/workspaces/${workspace.id}`);
      setEditor({
        draft: editableConfig(saved.collect_validation || emptyConfig),
        version: saved.version,
        dirty: false,
      });
      if (onSaved) await onSaved(saved);
      else await refresh();
    } catch (e) {
      setError(`重新载入失败：${e.message}`);
    } finally {
      setBusy("");
    }
  };
  const save = async () => {
    if (busy || stale || validateConfig(draft)) return;
    setBusy("save");
    setError("");
    try {
      const saved = await api(
        `/workspaces/${workspace.id}/collect-validation`,
        "POST",
        { ...configPayload(draft), version: editor.version },
      );
      setEditor({
        draft: editableConfig(saved.collect_validation || emptyConfig),
        version: saved.version,
        dirty: false,
      });
      notify(
        saved.collect_validation?.enabled
          ? "回传字段校验已启用"
          : "回传字段校验已关闭",
      );
      try {
        if (onSaved) await onSaved(saved);
        else await refresh();
      } catch (e) {
        setError(`配置已保存，但页面刷新失败：${e.message}`);
      }
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy("");
    }
  };

  return (
    <Panel title="回传字段校验" className="collect-validation-settings">
      <div className="padded">
        <p>
          校验当前工作区回传内容中 <code>data</code> 的字段、类型和长度。
          启用后，缺少必填字段或格式不符的提交返回 HTTP 422，且不保存回传。
        </p>
        <p className="collect-validation-note">
          HTTP 201 仅表示回传已接收，不证明执行过命令或数据来自真实主机。
          格式正确的虚构数据仍可能通过校验，执行状态仍为未验证。
        </p>
        <fieldset disabled={!!busy}>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={draft.enabled}
              onChange={(e) => {
                const enabled = e.target.checked;
                patch((current) => ({ ...current, enabled }));
              }}
            />
            启用回传字段校验
          </label>
          <p className="muted">
            最多 20 个字段；字段名区分大小写。IP 接受 IPv4 或
            IPv6，时间使用带时区的 RFC 3339 格式。
          </p>
          <div className="collect-validation-fields">
            {draft.fields.map((field, index) => (
              <div
                className="collect-validation-row"
                key={field.key}
                role="group"
                aria-label={`回传字段 ${index + 1}`}
              >
                <Field label={`字段 ${index + 1} 名称`}>
                  <input
                    value={field.name}
                    maxLength={64}
                    spellCheck="false"
                    placeholder="例如 host"
                    onChange={(e) =>
                      patchField(field.key, { name: e.target.value })
                    }
                  />
                </Field>
                <Field label={`字段 ${index + 1} 类型`}>
                  <Select
                    value={field.type}
                    disabled={!!busy}
                    onChange={(e) =>
                      patchField(field.key, { type: e.target.value })
                    }
                  >
                    <option value="string">文本</option>
                    <option value="ip">IP 地址</option>
                    <option value="datetime">时间</option>
                  </Select>
                </Field>
                <Field label={`字段 ${index + 1} 最大长度`}>
                  <input
                    type="number"
                    min={minimumLength(field.type)}
                    max={4096}
                    step={1}
                    value={field.max_length}
                    onChange={(e) =>
                      patchField(field.key, { max_length: e.target.value })
                    }
                  />
                </Field>
                <label className="checkbox-label collect-validation-required">
                  <input
                    type="checkbox"
                    checked={field.required}
                    aria-label={`字段 ${index + 1} 必填`}
                    onChange={(e) =>
                      patchField(field.key, { required: e.target.checked })
                    }
                  />
                  必填
                </label>
                <Button
                  icon={Trash2}
                  aria-label={`删除字段 ${index + 1}${field.name ? ` ${field.name}` : ""}`}
                  onClick={() =>
                    patch((current) => ({
                      ...current,
                      fields: current.fields.filter(
                        (item) => item.key !== field.key,
                      ),
                    }))
                  }
                >
                  删除
                </Button>
              </div>
            ))}
          </div>
          {!draft.fields.length && (
            <p className="muted">
              尚未配置字段，可添加字段或使用主机诊断预设。
            </p>
          )}
          <div className="actions collect-validation-actions">
            <Button
              icon={Plus}
              disabled={draft.fields.length >= 20}
              onClick={() => {
                const field = editableField({
                  name: "",
                  type: "string",
                  required: true,
                  max_length: 256,
                });
                patch((current) => ({
                  ...current,
                  fields: [...current.fields, field],
                }));
              }}
            >
              添加字段
            </Button>
            <Button
              onClick={() => {
                const fields = diagnosticFields.map(editableField);
                patch((current) => ({ ...current, fields }));
              }}
            >
              使用主机诊断预设（替换字段）
            </Button>
          </div>
        </fieldset>
        {stale && (
          <p className="composer-error" role="alert">
            工作区已有新版本，当前修改已保留。请重新载入已保存配置后编辑，以免覆盖其他修改。
          </p>
        )}
        {(error || validationError) && (
          <p className="composer-error" role="alert">
            {error || validationError}
          </p>
        )}
        <div className="actions collect-validation-actions">
          <Button
            icon={RotateCcw}
            loading={busy === "reload"}
            disabled={!!busy || !dirty}
            onClick={reloadSaved}
          >
            重新载入已保存配置（放弃修改）
          </Button>
          <Button
            icon={Save}
            variant="primary"
            loading={busy === "save"}
            disabled={!!busy || !dirty || stale || !!validationError}
            onClick={save}
          >
            保存字段校验
          </Button>
          {dirty && <small className="muted">未保存</small>}
        </div>
      </div>
    </Panel>
  );
}
