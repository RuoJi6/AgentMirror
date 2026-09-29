import { siteNodeName } from "./workspace-site-paths";
import {
  WorkspaceSites,
  HostedFiles,
  workspaceSiteNodes,
} from "./WorkspaceSites";
import React, {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  Plus,
  Save,
  Upload,
  Code2,
  Eye,
  Play,
  Pause,
  Send,
  Settings2,
  Trash2,
  Copy,
  ExternalLink,
  Layers,
  FileCode2,
  X,
  RefreshCw,
} from "lucide-react";
import { useApp } from "../App";
import { api } from "../api";
import Select from "../components/Select";
import ComposerAgent from "./ComposerAgent";
import HeaderFields from "./ComposerHeaders";
import RequestPreview from "./RequestPreview";
import WorkspaceReview from "./WorkspaceReview";
import FlowErrorBoundary from "./FlowErrorBoundary";
import CollectResponseSettings from "./CollectResponseSettings";
import CollectValidationSettings from "./CollectValidationSettings";
import SiteLibrary from "./SiteLibrary";
import MaterialCatalog from "./MaterialCatalog";
import ScenarioDeleteDialog from "./ScenarioDeleteDialog";
import { conditionLabel, previewRequestText } from "./preview-requests";
import {
  ordinaryResponse,
  responseHasPrompt,
  syncScenarioDelivery,
} from "./scenario-response";
import {
  WorkspaceList,
  WorkspaceIdentityDialog,
  WorkspaceToolbar,
} from "./WorkspaceShell";
import ModelProviders from "./ModelProviders";
import {
  Badge,
  Button,
  Callout,
  Confirm,
  CopyButton,
  Empty,
  Field,
  Modal,
  PageHeader,
  Panel,
} from "../components/UI";
import "./composer.css";
import "./workspace-shell.css";

const WorkspaceAccessFlow = lazy(() => import("./AccessFlow"));

const uid = (prefix) => `${prefix}_${Math.random().toString(36).slice(2, 10)}`;
const clone = (value) => structuredClone(value);
const newSite = () => ({
  name: "我的站点",
  entry: "index.html",
  spa: false,
  files: [
    {
      path: "index.html",
      encoding: "utf8",
      content:
        '<!doctype html>\n<html lang="zh-CN">\n<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>服务门户</title></head>\n<body><h1>服务门户</h1><a href="/api/info">查看服务信息</a></body>\n</html>',
    },
  ],
});
const responseDraft = () => ({
  status: 200,
  content_type: "text/plain; charset=utf-8",
  format: "text",
  body: "服务信息\n\n{{prompt}}",
  headers: {},
});
const newRule = () => ({
  id: uid("rule"),
  name: "服务信息",
  method: "GET",
  path: "/api/info",
  conditions: [],
  response: responseDraft(),
  delivery_required: true,
});
const newScenario = () => ({
  name: "自定义响应场景",
  description: "",
  rules: [newRule()],
});
const asNew = (value) => {
  const { id, version, created_at, updated_at, ...rest } = clone(value);
  return rest;
};
const methods = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
const mimeByFormat = {
  text: "text/plain; charset=utf-8",
  html: "text/html; charset=utf-8",
  json: "application/json; charset=utf-8",
};
const modelDefaults = { base_url: "", model: "", has_key: false };

export const GenerationSettings = ModelProviders;

function SiteEditor({ initial, onClose, onSave, draftOnly = false }) {
  const [draft, setDraft] = useState(() => clone(initial));
  const [selected, setSelected] = useState(0);
  const [filePath, setFilePath] = useState("");
  const [preview, setPreview] = useState(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const file = draft.files[selected];
  const patchFile = (patch) =>
    setDraft((s) => ({
      ...s,
      files: s.files.map((f, i) => (i === selected ? { ...f, ...patch } : f)),
    }));
  const act = async (action) => {
    setBusy(action);
    setError("");
    try {
      if (action === "preview") {
        const result = await api("/sites/preview", "POST", { site: draft });
        setPreview(result.html);
      } else {
        const result = draftOnly ? draft : await api("/sites", "POST", draft);
        await onSave(result);
      }
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy("");
    }
  };
  return (
    <Modal
      title={
        draftOnly
          ? "编辑任务结果 · 站点"
          : draft.id
            ? "编辑站点素材"
            : "新建站点素材"
      }
      wide
      className="composer-modal"
      onClose={onClose}
      footer={
        <>
          <span className="composer-footer-note">
            {draftOnly
              ? "先保留在任务结果中，采用后再保存到素材库。"
              : "保存到素材库，引用此素材的已发布端口会自动更新。"}
          </span>
          <Button onClick={onClose}>关闭</Button>
          <Button
            icon={Eye}
            disabled={!!busy}
            loading={busy === "preview"}
            onClick={() => act("preview")}
          >
            预览站点
          </Button>
          <Button
            icon={Save}
            variant="primary"
            disabled={!!busy}
            loading={busy === "save"}
            onClick={() => act("save")}
          >
            {draftOnly ? "保存到任务结果" : "保存站点素材"}
          </Button>
        </>
      }
    >
      <div className="composer-stack">
        <div className="form-grid">
          <Field label="站点名称">
            <input
              value={draft.name}
              onChange={(e) =>
                setDraft((s) => ({ ...s, name: e.target.value }))
              }
            />
          </Field>
          <Field label="首页文件">
            <Select
              value={draft.entry}
              onChange={(e) =>
                setDraft((s) => ({ ...s, entry: e.target.value }))
              }
            >
              {draft.files.map((f) => (
                <option key={f.path} value={f.path}>
                  {f.path}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        <Field
          label="素材来源（可选）"
          hint="记录开源项目地址、版本或作者，便于复用时核对授权。"
        >
          <input
            value={draft.source || ""}
            onChange={(e) =>
              setDraft((s) => ({ ...s, source: e.target.value }))
            }
          />
        </Field>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={!!draft.spa}
            onChange={(e) => setDraft((s) => ({ ...s, spa: e.target.checked }))}
          />
          单页应用：未匹配的页面路径回到首页
        </label>
        <div className="composer-file-editor">
          <aside aria-label="站点文件列表">
            {draft.files.map((f, i) => (
              <button
                key={i}
                className={selected === i ? "active" : ""}
                onClick={() => {
                  setSelected(i);
                  setPreview(null);
                }}
              >
                <FileCode2 size={15} />
                <span>{f.path}</span>
                {f.encoding === "base64" && <small>资源</small>}
              </button>
            ))}
            <div className="composer-add-file">
              <input
                aria-label="新文件路径"
                placeholder="assets/app.js"
                value={filePath}
                onChange={(e) => setFilePath(e.target.value)}
              />
              <Button
                icon={Plus}
                disabled={!filePath.trim()}
                onClick={() => {
                  const path = filePath.trim();
                  if (draft.files.some((f) => f.path === path)) {
                    setError("文件路径已存在");
                    return;
                  }
                  setDraft((s) => ({
                    ...s,
                    files: [
                      ...s.files,
                      { path, content: "", encoding: "utf8" },
                    ],
                  }));
                  setSelected(draft.files.length);
                  setFilePath("");
                  setPreview(null);
                }}
              >
                添加文件
              </Button>
            </div>
          </aside>
          <section>
            {file && (
              <>
                <div className="composer-file-toolbar">
                  <input
                    aria-label="当前文件路径"
                    value={file.path}
                    onChange={(e) => patchFile({ path: e.target.value })}
                  />
                  <Badge>{file.encoding || "utf8"}</Badge>
                  <button
                    className="icon-button"
                    disabled={draft.files.length <= 1}
                    aria-label="删除当前文件"
                    onClick={() => {
                      setDraft((s) => ({
                        ...s,
                        files: s.files.filter((_, i) => i !== selected),
                      }));
                      setSelected(0);
                    }}
                  >
                    <Trash2 size={16} />
                  </button>
                </div>
                {file.encoding === "hosted" ? (
                  <Callout>
                    托管下载文件 · {file.download_name || file.path}
                    。请在“文件托管与 HTTP 下载”中管理。
                  </Callout>
                ) : file.encoding === "base64" ? (
                  <Callout>
                    此文件以 Base64 保存。可替换编码内容，或重新导入资源包。
                  </Callout>
                ) : null}
                {file.encoding !== "hosted" && (
                  <textarea
                    className="composer-code"
                    aria-label="文件源码"
                    spellCheck={false}
                    value={file.content}
                    onChange={(e) => {
                      patchFile({ content: e.target.value });
                      setPreview(null);
                    }}
                  />
                )}
              </>
            )}
          </section>
        </div>
        {preview !== null && (
          <section className="composer-preview-panel">
            <h3>站点外观预览</h3>
            <iframe sandbox="" title="站点素材预览" srcDoc={preview} />
            <small>
              静态外观预览不执行页面脚本，也不调用测试接口。完整响应及提示词在工作区的请求预演中检查。
            </small>
          </section>
        )}
        {error && (
          <p role="alert" className="composer-error">
            {error}
          </p>
        )}
      </div>
    </Modal>
  );
}

function ResponseEditor({ value, onChange, fallback = false }) {
  const bodyRef = useRef(null);
  const patch = (values) => onChange({ ...value, ...values });
  const insertPrompt = () => {
    const node = bodyRef.current;
    const at = node?.selectionStart ?? value.body.length;
    const end = node?.selectionEnd ?? at;
    patch({
      body: value.body.slice(0, at) + "{{prompt}}" + value.body.slice(end),
    });
    node?.focus();
  };
  return (
    <div className="composer-response">
      <div className="composer-three-fields">
        <Field label={fallback ? "默认状态码" : "命中状态码"}>
          <input
            type="number"
            min="200"
            max="599"
            value={value.status}
            onChange={(e) => patch({ status: Number(e.target.value) })}
          />
        </Field>
        <Field label={fallback ? "默认响应格式" : "响应格式"}>
          <Select
            value={value.format}
            onChange={(e) =>
              patch({
                format: e.target.value,
                content_type: mimeByFormat[e.target.value],
                prompt_prefix:
                  e.target.value === "text" ? value.prompt_prefix || "" : "",
              })
            }
          >
            <option value="text">纯文本</option>
            <option value="html">HTML</option>
            <option value="json">JSON</option>
          </Select>
        </Field>
        <Field label="Content-Type">
          <input
            value={value.content_type}
            onChange={(e) => patch({ content_type: e.target.value })}
          />
        </Field>
      </div>
      <div className="composer-section-line">
        <strong>{fallback ? "未命中时的返回正文" : "命中后的返回正文"}</strong>
        {!fallback && (
          <Button icon={Plus} variant="ghost" onClick={insertPrompt}>
            插入提示词槽位
          </Button>
        )}
      </div>
      <textarea
        ref={bodyRef}
        className="composer-code response-code"
        aria-label={fallback ? "默认响应正文" : "命中响应正文"}
        spellCheck={false}
        value={value.body}
        onChange={(e) => patch({ body: e.target.value })}
      />
      <small className="muted">
        {fallback
          ? "默认响应用于未触发请求，请填写普通返回内容，不包含提示词槽位。"
          : '普通响应直接填写返回内容，无需配置提示词。插入 {{prompt}} 后自动启用交付；JSON 放在字符串值内，例如 {"message":"{{prompt}}"}，HTML 放在文本节点中。'}
      </small>
      {fallback && responseHasPrompt(value) && (
        <div role="alert" className="composer-error">
          未命中时的默认响应只能返回普通内容，请移除提示词槽位。
          <Button
            variant="ghost"
            onClick={() => onChange(ordinaryResponse(value))}
          >
            移除默认响应中的槽位
          </Button>
        </div>
      )}
      {value.format === "text" && !fallback && responseHasPrompt(value) && (
        <Field
          label="提示词每行前缀（可选）"
          hint="保留输入的空格。例如 # 加空格作为 YAML 注释，两个空格用于缩进。"
        >
          <input
            value={value.prompt_prefix || ""}
            onChange={(e) => patch({ prompt_prefix: e.target.value })}
            placeholder="# "
          />
        </Field>
      )}
      <HeaderFields
        value={value.headers}
        onChange={(headers) => patch({ headers })}
      />
    </div>
  );
}

function ScenarioEditor({ initial, onClose, onSave, draftOnly = false }) {
  const [draft, setDraft] = useState(() =>
    syncScenarioDelivery(clone(initial)),
  );
  const [index, setIndex] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const rule = draft.rules[index];
  const patchRule = (patch) => {
    setError("");
    setDraft((s) => ({
      ...s,
      rules: s.rules.map((r, i) =>
        i === index
          ? {
              ...r,
              ...patch,
              ...(patch.response && {
                delivery_required: responseHasPrompt(patch.response),
              }),
            }
          : r,
      ),
    }));
  };
  const patchCondition = (i, patch) =>
    patchRule({
      conditions: rule.conditions.map((c, n) =>
        n === i ? { ...c, ...patch } : c,
      ),
    });
  return (
    <Modal
      title={
        draftOnly
          ? "编辑任务结果 · 场景"
          : draft.id
            ? "编辑模拟场景"
            : "新建模拟场景"
      }
      wide
      className="composer-modal"
      onClose={onClose}
      footer={
        <>
          <span className="composer-footer-note">
            {draftOnly
              ? "先保留在任务结果中，采用后再保存到素材库。"
              : "场景可重复挂载，路径和提示词在工作区独立绑定。"}
          </span>
          <Button onClick={onClose}>关闭</Button>
          <Button
            variant="primary"
            icon={Save}
            loading={busy}
            onClick={async () => {
              setBusy(true);
              setError("");
              try {
                const payload = syncScenarioDelivery(draft);
                await onSave(
                  draftOnly
                    ? payload
                    : await api("/scenarios", "POST", payload),
                );
              } catch (e) {
                setError(e.message);
              } finally {
                setBusy(false);
              }
            }}
          >
            {draftOnly ? "保存到任务结果" : "保存模拟场景"}
          </Button>
        </>
      }
    >
      <div className="composer-stack">
        <div className="form-grid">
          <Field label="场景名称">
            <input
              value={draft.name}
              onChange={(e) =>
                setDraft((s) => ({ ...s, name: e.target.value }))
              }
            />
          </Field>
          <Field label="场景说明">
            <input
              value={draft.description || ""}
              onChange={(e) =>
                setDraft((s) => ({ ...s, description: e.target.value }))
              }
            />
          </Field>
        </div>
        <div className="composer-section-line">
          <div
            className="composer-rule-tabs"
            role="tablist"
            aria-label="场景规则"
          >
            {draft.rules.map((r, i) => (
              <button
                role="tab"
                aria-selected={index === i}
                className={index === i ? "active" : ""}
                key={r.id}
                onClick={() => setIndex(i)}
              >
                {r.name || `规则 ${i + 1}`}
              </button>
            ))}
          </div>
          <Button
            icon={Plus}
            disabled={draft.rules.length >= 64}
            onClick={() => {
              setDraft((s) => ({
                ...s,
                rules: [
                  ...s.rules,
                  {
                    ...newRule(),
                    name: `规则 ${s.rules.length + 1}`,
                    path: `/api/route-${s.rules.length + 1}`,
                  },
                ],
              }));
              setIndex(draft.rules.length);
            }}
          >
            添加规则
          </Button>
        </div>
        {rule && (
          <>
            <Callout>
              同一路径可以按参数返回不同内容。例如路径填写 /api/download， 用
              URL 参数 file 等于 index.html 或 ../.././etc/passwd
              区分规则。普通响应直接填写正文；插入提示词槽位后自动启用交付。
              规则从左到右取首个命中；全部未命中时才使用默认响应。
            </Callout>
            <div className="composer-three-fields">
              <Field label="规则名称">
                <input
                  value={rule.name}
                  onChange={(e) => patchRule({ name: e.target.value })}
                />
              </Field>
              <Field label="请求方法">
                <Select
                  value={rule.method}
                  onChange={(e) => patchRule({ method: e.target.value })}
                >
                  {methods
                    .filter((m) => m !== "HEAD")
                    .map((m) => (
                      <option value={m} key={m}>
                        {m}
                      </option>
                    ))}
                </Select>
              </Field>
              <Field label="请求路径">
                <input
                  value={rule.path}
                  placeholder="/api/info"
                  onChange={(e) => patchRule({ path: e.target.value })}
                />
              </Field>
            </div>
            <div className="composer-section-line">
              <strong>
                命中条件{" "}
                <small className="muted">
                  全部满足；留空表示该路径所有请求
                </small>
              </strong>
              <Button
                icon={Plus}
                variant="ghost"
                onClick={() =>
                  patchRule({
                    conditions: [
                      ...rule.conditions,
                      {
                        source: "query",
                        key: "q",
                        operator: "contains",
                        value: "",
                      },
                    ],
                  })
                }
              >
                添加条件
              </Button>
            </div>
            {rule.conditions.map((condition, i) => (
              <div className="composer-condition" key={i}>
                <Select
                  aria-label={`条件 ${i + 1} 来源`}
                  value={condition.source}
                  onChange={(e) =>
                    patchCondition(i, {
                      source: e.target.value,
                      ...(e.target.value === "body" ? { key: "" } : {}),
                    })
                  }
                >
                  <option value="query">URL 参数</option>
                  <option value="header">请求头</option>
                  <option value="form">表单</option>
                  <option value="json">JSON 字段</option>
                  <option value="body">整个正文</option>
                </Select>
                <input
                  aria-label={`条件 ${i + 1} 字段`}
                  placeholder="字段名"
                  disabled={condition.source === "body"}
                  value={condition.key}
                  onChange={(e) => patchCondition(i, { key: e.target.value })}
                />
                <Select
                  aria-label={`条件 ${i + 1} 操作`}
                  value={condition.operator}
                  onChange={(e) =>
                    patchCondition(i, { operator: e.target.value })
                  }
                >
                  <option value="equals">等于</option>
                  <option value="contains">包含</option>
                  <option value="exists">存在</option>
                </Select>
                <input
                  aria-label={`条件 ${i + 1} 值`}
                  placeholder="匹配内容"
                  value={condition.value || ""}
                  disabled={condition.operator === "exists"}
                  onChange={(e) => patchCondition(i, { value: e.target.value })}
                />
                <button
                  className="icon-button"
                  aria-label={`删除条件 ${i + 1}`}
                  onClick={() =>
                    patchRule({
                      conditions: rule.conditions.filter((_, n) => n !== i),
                    })
                  }
                >
                  <X size={16} />
                </button>
              </div>
            ))}
            <ResponseEditor
              key={`${rule.id}-response`}
              value={rule.response}
              onChange={(response) => patchRule({ response })}
            />
            <div className="composer-section-line" aria-live="polite">
              <span className="muted">
                <Badge>
                  {responseHasPrompt(rule.response) ? "交付提示词" : "普通响应"}
                </Badge>{" "}
                {responseHasPrompt(rule.response)
                  ? "命中时在槽位返回该接口绑定的提示词。"
                  : "命中时返回上面填写的正文，不需要绑定提示词。"}
              </span>
              {responseHasPrompt(rule.response) && (
                <Button
                  variant="ghost"
                  onClick={() =>
                    patchRule({ response: ordinaryResponse(rule.response) })
                  }
                >
                  改为普通响应
                </Button>
              )}
            </div>
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={!!rule.fallback}
                onChange={(e) =>
                  patchRule({
                    fallback: e.target.checked
                      ? { ...responseDraft(), body: "未满足请求条件" }
                      : undefined,
                  })
                }
              />
              为条件未命中的请求设置默认响应
            </label>
            {rule.fallback && (
              <ResponseEditor
                key={`${rule.id}-fallback`}
                value={rule.fallback}
                fallback
                onChange={(fallback) => patchRule({ fallback })}
              />
            )}
            <div className="actions">
              <Button
                icon={Copy}
                disabled={draft.rules.length >= 64}
                onClick={() => {
                  const copied = {
                    ...clone(rule),
                    id: uid("rule"),
                    name: `${rule.name || "规则"}（副本）`,
                  };
                  setDraft((s) => ({
                    ...s,
                    rules: [
                      ...s.rules.slice(0, index + 1),
                      copied,
                      ...s.rules.slice(index + 1),
                    ],
                  }));
                  setIndex(index + 1);
                }}
              >
                复制规则
              </Button>
              <Button
                icon={Trash2}
                variant="ghost danger-text"
                disabled={draft.rules.length <= 1}
                onClick={() => {
                  setDraft((s) => ({
                    ...s,
                    rules: s.rules.filter((_, i) => i !== index),
                  }));
                  setIndex(0);
                }}
              >
                删除此规则
              </Button>
            </div>
            <p className="muted">
              可复制当前规则，再修改参数值和返回正文，快速增加文件分支。
              “等于”精确比较一次 URL 解码后的值，../ 和 ./ 会原样保留。
              默认响应可统一设置在第一条规则；后续匹配规则仍会优先返回。
            </p>
          </>
        )}
        {error && (
          <p role="alert" className="composer-error">
            {error}
          </p>
        )}
      </div>
    </Modal>
  );
}

function PublishPort({ deployment, onClose }) {
  const { data, refresh, notify } = useApp();
  const [existing, setExisting] = useState("");
  const workspace = data.workspaces.find(
    (w) => w.id === deployment.workspace_id,
  );
  const [siteNodeID, setSiteNodeID] = useState(
    deployment.selected_site_node_id || "root",
  );
  const nodes = workspaceSiteNodes(workspace || {});
  const usedPorts = (data.listeners || []).map((l) => Number(l.port));
  let defaultPort =
    Math.max(
      data.runtime.public_port || 8765,
      data.runtime.admin_port || 8766,
      8766,
    ) + 1;
  while (usedPorts.includes(defaultPort)) defaultPort++;
  if (defaultPort > 65535) {
    defaultPort = 8767;
    while (usedPorts.includes(defaultPort)) defaultPort++;
  }
  const initialURL = new URL(data.settings.public_url);
  initialURL.protocol = "http:";
  initialURL.port = String(defaultPort);
  const [port, setPort] = useState({
    name: deployment.name,
    host: "0.0.0.0",
    port: defaultPort,
    public_url: initialURL.origin,
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <Modal
      title="发布到测试端口"
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>稍后绑定</Button>
          <Button
            variant="primary"
            icon={Send}
            loading={busy}
            onClick={async () => {
              setBusy(true);
              setError("");
              try {
                const item = existing
                  ? data.listeners.find((l) => l.id === existing)
                  : port;
                await api("/listeners", "POST", {
                  ...item,
                  deployment_id: deployment.id,
                  site_node_id: siteNodeID,
                  root_surface: "page",
                  overrides: null,
                  enabled: true,
                });
                await refresh();
                notify("发布版本已绑定端口，测试入口已生效");
                onClose();
              } catch (e) {
                setError(e.message);
              } finally {
                setBusy(false);
              }
            }}
          >
            绑定并启用端口
          </Button>
        </>
      }
    >
      <div className="composer-stack">
        <Callout>
          工作区已发布。以后保存站点、场景或提示词绑定，会自动更新所有已绑定端口；已有会话的后续请求也使用新内容，历史记录保留。下方可额外绑定测试端口。
        </Callout>
        <Field
          label="端口首页站点"
          hint="所选站点从此端口的 / 访问；其接口与文件使用站内路径。"
        >
          <Select
            value={siteNodeID}
            onChange={(e) => setSiteNodeID(e.target.value)}
          >
            {nodes.map((n) => (
              <option key={n.id} value={n.id}>
                {n.name} · {n.mount_path}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="测试端口">
          <Select
            value={existing}
            onChange={(e) => setExisting(e.target.value)}
          >
            <option value="">创建新端口</option>
            {(data.listeners || []).map((l) => (
              <option value={l.id} key={l.id}>
                {l.name} · {l.port}
              </option>
            ))}
          </Select>
        </Field>
        {existing ? (
          <Callout>
            此次绑定会把所选端口的首页切换为「
            {siteNodeName(workspace || {}, siteNodeID)}
            」。该端口原有访问入口将被替换。
          </Callout>
        ) : (
          <>
            <div className="form-grid">
              <Field label="端口名称">
                <input
                  value={port.name}
                  onChange={(e) =>
                    setPort((p) => ({ ...p, name: e.target.value }))
                  }
                />
              </Field>
              <Field label="监听地址">
                <input
                  value={port.host}
                  onChange={(e) =>
                    setPort((p) => ({ ...p, host: e.target.value }))
                  }
                />
              </Field>
              <Field label="监听端口">
                <input
                  type="number"
                  min="1"
                  max="65535"
                  value={port.port}
                  onChange={(e) => {
                    const n = Number(e.target.value);
                    setPort((p) => {
                      let public_url = p.public_url;
                      try {
                        const u = new URL(public_url);
                        u.port = String(n);
                        public_url = u.origin;
                      } catch {}
                      return { ...p, port: n, public_url };
                    });
                  }}
                />
              </Field>
              <Field
                label="公告地址"
                hint="填写被测设备可访问的 IP / 域名及端口。"
              >
                <input
                  value={port.public_url}
                  onChange={(e) =>
                    setPort((p) => ({ ...p, public_url: e.target.value }))
                  }
                />
              </Field>
            </div>
          </>
        )}
        {error && (
          <p role="alert" className="composer-error">
            {error}
          </p>
        )}
      </div>
    </Modal>
  );
}

export default function Composer() {
  const { data, route, navigate, guardNavigation, notify, refresh } = useApp();
  const [library, setLibrary] = useState(null);
  const [workspace, setWorkspace] = useState(null);
  const [tab, setTab] = useState("assistant");
  const [identityOpen, setIdentityOpen] = useState(false);
  const [renamingWorkspace, setRenamingWorkspace] = useState(null);
  const [siteLibraryOpen, setSiteLibraryOpen] = useState(false);
  const [pendingLeave, setPendingLeave] = useState(null);
  const [siteEditor, setSiteEditor] = useState(null);
  const [scenarioEditor, setScenarioEditor] = useState(null);
  const [deletingScenario, setDeletingScenario] = useState(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [reviewSettingsOpen, setReviewSettingsOpen] = useState(false);
  const [requestedReviewJob, setRequestedReviewJob] = useState("");
  const [model, setModel] = useState(modelDefaults);
  const [modelError, setModelError] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [portDeployment, setPortDeployment] = useState(null);
  const [deletingWorkspace, setDeletingWorkspace] = useState(null);
  const [previewTarget, setPreviewTarget] = useState(null);
  const routeLoaded = useRef(null);
  const uploadRef = useRef(null);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const libraryRef = useRef(library);
  libraryRef.current = library;
  const reload = useCallback(async () => {
    const previous = libraryRef.current;
    const state = await api("/composer/state");
    if (alive.current) {
      setWorkspace((current) => {
        const before = previous?.workspaces.find(
          (item) => item.id === current?.id,
        );
        const after = state.workspaces.find((item) => item.id === current?.id);
        return before &&
          after &&
          JSON.stringify(current) === JSON.stringify(before)
          ? clone(after)
          : current;
      });
      setLibrary(state);
    }
    return state;
  }, []);
  useEffect(() => {
    reload().catch((e) => setError(e.message));
    api("/generation/settings")
      .then(setModel)
      .catch((e) => setModelError(e.message));
  }, [reload]);
  useEffect(() => {
    if (!library || routeLoaded.current === (route.id || "list")) return;
    routeLoaded.current = route.id || "list";
    setError("");
    setTab("assistant");
    setPreviewTarget(null);
    setIdentityOpen(false);
    setReviewSettingsOpen(false);
    setRequestedReviewJob("");
    if (route.id === "new") setWorkspace(null);
    else if (route.id) {
      const found = library.workspaces.find(
        (w) => w.id === route.id || w.deployment_id === route.id,
      );
      setWorkspace(found ? clone(found) : null);
      if (!found) setError("找不到此工作区，请从列表选择。");
    } else setWorkspace(null);
  }, [route.id, library]);
  const patchWorkspace = (patch) => {
    setWorkspace((w) => (w?.id === workspace?.id ? { ...w, ...patch } : w));
  };
  const savedWorkspace = library?.workspaces.find(
    (item) => item.id === workspace?.id,
  );
  const dirty =
    !!workspace && JSON.stringify(workspace) !== JSON.stringify(savedWorkspace);
  useEffect(() => {
    if (!dirty) return;
    return guardNavigation((proceed, next) => {
      if (next.page === "composer" && next.id === workspace.id) return true;
      setPendingLeave(() => proceed);
      return false;
    });
  }, [dirty, guardNavigation, workspace?.id]);
  useEffect(() => {
    if (!dirty) return;
    const warn = (event) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  const site = library?.sites.find((s) => s.id === workspace?.site_id);
  const run = async (action, fn) => {
    setBusy(action);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(e.message);
    } finally {
      if (alive.current) setBusy("");
    }
  };
  const saveWorkspace = async (draft = workspace) => {
    const saved = await api("/workspaces", "POST", draft);
    if (!alive.current) return saved;
    setWorkspace(saved);
    await Promise.all([reload(), refresh()]);
    if (!alive.current) return saved;
    routeLoaded.current = saved.id;
    navigate("composer", saved.id);
    return saved;
  };
  const setPublication = (target, enabled) =>
    run("status", async () => {
      const updated = await api(`/workspaces/${target.id}/status`, "POST", {
        enabled,
        version: target.version,
      });
      setWorkspace((current) =>
        current?.id === target.id
          ? {
              ...current,
              version: updated.version,
              publication_status: updated.publication_status,
              enabled: updated.enabled,
            }
          : current,
      );
      await Promise.all([reload(), refresh()]);
      notify(
        enabled
          ? "发布已恢复，已启用端口恢复提供测试内容"
          : "发布已暂停，端口绑定和历史会话已保留",
      );
    });
  const copyWorkspace = (target) =>
    run("copy", async () => {
      const saved = await api("/workspaces", "POST", {
        name: `${[...target.name].slice(0, 70).join("")} · 副本`,
        slug: `${target.slug.slice(0, 45)}-copy-${Math.random().toString(36).slice(2, 8)}`,
        site_id: target.site_id,
        site_mounts: clone(target.site_mounts || []),
        callback_path: target.callback_path || "/collect",
        server_header: target.server_header || "",
        collect_rejected_response: clone(target.collect_rejected_response),
        collect_validation: clone(
          target.collect_validation || { enabled: false, fields: [] },
        ),
        bindings: clone(target.bindings || []),
      });
      await Promise.all([reload(), refresh()]);
      if (!alive.current) return;
      navigate("composer", saved.id);
      notify("工作区已复制为未发布草稿，复用原站点和场景素材");
    });
  const addBinding = (scenario) => {
    patchWorkspace({
      bindings: [
        ...(workspace.bindings || []),
        {
          id: uid("binding"),
          scenario_id: scenario.id,
          profile_id: data.profiles[0]?.id || "",
          paths: {},
          enabled: true,
        },
      ],
    });
    setTab("bindings");
  };
  const editBinding = (id, patch) =>
    patchWorkspace({
      bindings: workspace.bindings.map((b) =>
        b.id === id ? { ...b, ...patch } : b,
      ),
    });
  const published = data.deployments.find(
    (d) => d.id === workspace?.deployment_id,
  );
  const publicationPaused =
    workspace?.publication_status === "paused" || published?.enabled === false;
  const listeners = (data.listeners || []).filter(
    (l) => l.deployment_id === workspace?.deployment_id,
  );
  const presetSites = library?.presets?.sites || [];
  const presetScenarios = library?.presets?.scenarios || [];
  const importZip = async (file) => {
    if (!file) return;
    await run("import", async () => {
      if (file.size > 8 * 1024 * 1024)
        throw new Error("ZIP 文件不能超过 8 MiB。");
      const encoded = await new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result).split(",")[1]);
        reader.onerror = () => reject(new Error("读取 ZIP 失败"));
        reader.readAsDataURL(file);
      });
      const imported = await api("/sites/import", "POST", {
        name: file.name.replace(/\.zip$/i, ""),
        zip_base64: encoded,
      });
      await reload();
      patchWorkspace({ site_id: imported.id });
      setSiteEditor(imported);
      notify("资源包已导入，可编辑首页和文件后保存");
    });
    if (uploadRef.current) uploadRef.current.value = "";
  };
  if (!library)
    return (
      <>
        <PageHeader
          title="蜜罐工作区"
          description="自由组合站点、模拟响应和指定提示词。"
        />
        {error ? (
          <Callout>
            {error}
            <Button onClick={() => run("load", reload)}>重试加载</Button>
          </Callout>
        ) : (
          <p className="muted">正在载入工作区…</p>
        )}
      </>
    );
  return (
    <>
      {error && (
        <div role="alert" className="composer-error">
          {error}
        </div>
      )}
      {!workspace ? (
        <WorkspaceList
          workspaces={library.workspaces}
          sites={library.sites}
          profiles={data.profiles}
          scenarios={library.scenarios}
          busy={!!busy}
          onCreate={() => navigate("composer", "new")}
          onOpen={(item) => navigate("composer", item.id)}
          onCopy={copyWorkspace}
          onRename={setRenamingWorkspace}
          onSites={() => setSiteLibraryOpen(true)}
          onDelete={setDeletingWorkspace}
          onPublication={setPublication}
          onModels={() => setSettingsOpen(true)}
          onRefresh={() =>
            run("reload", async () => {
              await Promise.all([reload(), refresh()]);
              notify("素材库已刷新");
            })
          }
        />
      ) : (
        <div className="workspace-editor">
          <WorkspaceToolbar
            workspace={workspace}
            dirty={dirty}
            busy={busy}
            site={site}
            onBack={() => navigate("composer")}
            onSettings={() => setIdentityOpen(true)}
            onRename={() => setRenamingWorkspace(workspace)}
            onSave={() =>
              run("save", async () => {
                const saved = await saveWorkspace();
                notify(
                  saved.deployment_id
                    ? "工作区已保存，站点、场景与提示词绑定已同步到已发布端口"
                    : "工作区草稿已保存",
                );
              })
            }
            onPublish={() =>
              run("publish", async () => {
                const saved = dirty ? await saveWorkspace() : workspace;
                if (!alive.current) return;
                const deployment = await api(
                  `/workspaces/${saved.id}/publish`,
                  "POST",
                  { version: saved.version },
                );
                const updated = await reload();
                if (!alive.current) return;
                setWorkspace(
                  clone(
                    updated.workspaces.find((item) => item.id === saved.id) ||
                      saved,
                  ),
                );
                await refresh();
                notify("工作区已发布，绑定测试端口后即可访问");
                setTab("publish");
                setPortDeployment(deployment);
              })
            }
          />
          <div
            className="tabs composer-tabs workspace-editor-tabs"
            role="tablist"
            aria-label="工作区步骤"
          >
            {[
              ["assistant", "AI 助手"],
              ["site", "站点素材"],
              ["scenarios", "模拟场景"],
              ["bindings", "绑定提示词"],
              ["preview", "请求预演"],
              ["flow", "访问画板"],
              ["publish", "发布设置"],
            ].map(([key, name]) => (
              <button
                role="tab"
                id={`workspace-tab-${key}`}
                aria-controls={`workspace-view-${key}`}
                aria-selected={tab === key}
                className={tab === key ? "active" : ""}
                key={key}
                onClick={() => setTab(key)}
              >
                {name}
              </button>
            ))}
          </div>
          <div className="workspace-editor-body">
            {tab === "flow" && (
              <div
                id="workspace-view-flow"
                role="tabpanel"
                aria-labelledby="workspace-tab-flow"
              >
                <FlowErrorBoundary
                  key={workspace.id}
                  onBack={() => setTab("assistant")}
                  beforeRefresh={dirty ? () => saveWorkspace() : undefined}
                >
                  <Suspense
                    fallback={<p className="padded muted">正在加载访问画板…</p>}
                  >
                    <WorkspaceAccessFlow
                      key={workspace.id}
                      workspace={workspace}
                      dirty={dirty}
                      onConversation={(id) => {
                        setRequestedReviewJob(id);
                        setTab("assistant");
                      }}
                    />
                  </Suspense>
                </FlowErrorBoundary>
              </div>
            )}
            {reviewSettingsOpen && (
              <Modal
                title="核查触发设置"
                wide
                onClose={() => setReviewSettingsOpen(false)}
              >
                <WorkspaceReview
                  workspace={workspace}
                  dirty={dirty}
                  onStarted={(job) => {
                    setRequestedReviewJob(job.id);
                    setReviewSettingsOpen(false);
                    setTab("assistant");
                  }}
                />
              </Modal>
            )}
            <div
              className="workspace-assistant-view"
              id="workspace-view-assistant"
              role="tabpanel"
              aria-labelledby="workspace-tab-assistant"
              hidden={tab !== "assistant"}
            >
              <ComposerAgent
                key={workspace.id}
                workspaceName={workspace.name}
                workspaceId={workspace.id}
                site={site}
                sites={library.sites}
                bindings={workspace.bindings || []}
                scenarios={library.scenarios}
                profiles={data.profiles}
                model={model}
                modelError={modelError}
                onSettings={() => setSettingsOpen(true)}
                onReviewSettings={() => setReviewSettingsOpen(true)}
                requestedJobId={requestedReviewJob}
                renderSiteEditor={(props) => <SiteEditor {...props} />}
                renderScenarioEditor={(props) => <ScenarioEditor {...props} />}
                onMaterialsChanged={async () => {
                  await reload();
                  await refresh();
                }}
                onAdopt={async (material, bindingId) => {
                  await reload();
                  if (!alive.current) return;
                  setWorkspace((current) => {
                    if (!current || current.id !== workspace.id) return current;
                    let bindings = current.bindings || [];
                    if (material.scenario) {
                      const validRuleIds = new Set(
                        material.scenario.rules.map((rule) => rule.id),
                      );
                      const target = bindings.find(
                        (binding) => binding.id === bindingId,
                      );
                      bindings = target
                        ? bindings.map((binding) =>
                            binding.id === bindingId
                              ? {
                                  ...binding,
                                  scenario_id: material.scenario.id,
                                  profile_id:
                                    material.profile_id || binding.profile_id,
                                  paths: Object.fromEntries(
                                    Object.entries(binding.paths || {}).filter(
                                      ([id]) => validRuleIds.has(id),
                                    ),
                                  ),
                                  rule_profiles: Object.fromEntries(
                                    Object.entries(
                                      binding.rule_profiles || {},
                                    ).filter(([id]) => validRuleIds.has(id)),
                                  ),
                                }
                              : binding,
                          )
                        : [
                            ...bindings,
                            {
                              id: uid("binding"),
                              scenario_id: material.scenario.id,
                              profile_id:
                                material.profile_id ||
                                data.profiles[0]?.id ||
                                "",
                              paths: {},
                              enabled: true,
                            },
                          ];
                    } else if (material.profile_id && bindingId) {
                      bindings = bindings.map((binding) =>
                        binding.id === bindingId
                          ? { ...binding, profile_id: material.profile_id }
                          : binding,
                      );
                    }
                    return {
                      ...current,
                      ...(material.site ? { site_id: material.site.id } : {}),
                      bindings,
                    };
                  });
                  notify(
                    material.profile_id &&
                      !material.scenario &&
                      !workspace.bindings?.some(
                        (binding) => binding.id === bindingId,
                      )
                      ? "站点已采用；尚无目标模拟场景，请在绑定提示词步骤添加场景并选择所引用的方案"
                      : material.profile_id
                        ? `已采用并绑定提示词「${material.profile?.name || "所选方案"}」，请保存工作区并预演；已发布端口随保存更新`
                        : "任务结果已采用到当前草稿，请保存工作区；已有测试入口将随保存更新",
                  );
                }}
              />
            </div>
            {tab === "site" && (
              <div
                className="workspace-config-view"
                id="workspace-view-site"
                role="tabpanel"
                aria-labelledby="workspace-tab-site"
              >
                <Panel
                  title="站点素材"
                  className="composer-content"
                  action={
                    <div className="actions">
                      <Button
                        icon={FileCode2}
                        onClick={() => setSiteLibraryOpen(true)}
                      >
                        管理素材
                      </Button>
                      <Button
                        icon={Upload}
                        loading={busy === "import"}
                        onClick={() => uploadRef.current?.click()}
                      >
                        导入 ZIP
                      </Button>
                      <Button
                        icon={Plus}
                        onClick={() => setSiteEditor(newSite())}
                      >
                        新建站点
                      </Button>
                      <input
                        ref={uploadRef}
                        className="composer-file-input"
                        type="file"
                        accept=".zip,application/zip"
                        aria-label="导入站点 ZIP"
                        onChange={(e) => importZip(e.target.files[0])}
                      />
                    </div>
                  }
                >
                  <MaterialCatalog
                    kind="site"
                    items={library.sites}
                    workspace={workspace}
                    workspaces={library.workspaces}
                    notify={notify}
                    onEdit={setSiteEditor}
                    onCopy={(target) =>
                      setSiteEditor({
                        ...asNew(target),
                        name: `${target.name} · 副本`,
                      })
                    }
                    onSelect={(target) => {
                      patchWorkspace({ site_id: target?.id || "" });
                      if (!target)
                        notify("已移除当前选用，保存工作区后解除引用");
                    }}
                  />
                  <details
                    className="material-presets"
                    open={library.sites.length === 0 ? true : undefined}
                  >
                    <summary>从预设开始 · {presetSites.length} 个预设</summary>
                    <div className="composer-preset-grid">
                      {presetSites.map((preset, i) => (
                        <button
                          key={preset.id || i}
                          onClick={() => setSiteEditor(asNew(preset))}
                        >
                          <strong>{preset.name}</strong>
                          <span>
                            {preset.description ||
                              `${preset.files?.length || 0} 个文件`}
                          </span>
                        </button>
                      ))}
                    </div>
                  </details>
                  <Callout>
                    支持 HTML / CSS / JavaScript 和静态资源；开源 React / Vue
                    模板请导入构建后的 dist ZIP。将前端表单 action、链接或 fetch
                    路径与模拟场景路径对应即可。
                  </Callout>
                </Panel>
                <WorkspaceSites
                  workspace={workspace}
                  sites={library.sites}
                  patch={patchWorkspace}
                  onEdit={setSiteEditor}
                  notify={notify}
                  listeners={listeners}
                  canBind={
                    !!workspace.deployment_id && !publicationPaused && !dirty
                  }
                  onBind={(nodeID) =>
                    setPortDeployment({
                      ...published,
                      id: workspace.deployment_id,
                      name: workspace.name,
                      workspace_id: workspace.id,
                      selected_site_node_id: nodeID,
                    })
                  }
                />
                <HostedFiles
                  key={workspace.id}
                  workspace={workspace}
                  dirty={dirty}
                  sites={library.sites}
                  refresh={async () => {
                    await reload();
                    await refresh();
                  }}
                  notify={notify}
                  listeners={listeners}
                />
              </div>
            )}
            {tab === "scenarios" && (
              <div
                className="workspace-config-view"
                id="workspace-view-scenarios"
                role="tabpanel"
                aria-labelledby="workspace-tab-scenarios"
              >
                <Panel
                  title="可复用的模拟场景"
                  className="composer-content"
                  action={
                    <Button
                      icon={Plus}
                      onClick={() => setScenarioEditor(newScenario())}
                    >
                      新建模拟场景
                    </Button>
                  }
                >
                  <MaterialCatalog
                    kind="scenario"
                    items={library.scenarios}
                    workspace={workspace}
                    workspaces={library.workspaces}
                    notify={notify}
                    onEdit={setScenarioEditor}
                    onCopy={(target) =>
                      setScenarioEditor({
                        ...asNew(target),
                        name: `${target.name} · 副本`,
                      })
                    }
                    onDelete={setDeletingScenario}
                    onAdd={addBinding}
                  />
                  <details
                    className="material-presets"
                    open={library.scenarios.length === 0 ? true : undefined}
                  >
                    <summary>
                      预设场景 · {presetScenarios.length} 个预设
                    </summary>
                    <div className="composer-preset-grid">
                      {presetScenarios.map((preset, i) => (
                        <button
                          key={preset.id || i}
                          onClick={() => setScenarioEditor(asNew(preset))}
                        >
                          <strong>{preset.name}</strong>
                          <span>
                            {preset.description ||
                              `${preset.rules?.length || 0} 条规则`}
                          </span>
                        </button>
                      ))}
                    </div>
                  </details>
                </Panel>
              </div>
            )}
            {tab === "bindings" && (
              <div
                className="workspace-config-view"
                id="workspace-view-bindings"
                role="tabpanel"
                aria-labelledby="workspace-tab-bindings"
              >
                <Panel
                  title="场景实例与提示词绑定"
                  className="composer-content"
                  action={
                    <Button icon={Plus} onClick={() => setTab("scenarios")}>
                      添加场景实例
                    </Button>
                  }
                >
                  <p className="muted">
                    先设置场景默认提示词，再为需要的接口单独指定。相同路径的不同参数规则也可分别绑定；请求从上到下匹配。
                  </p>
                  {!workspace.bindings.length ? (
                    <Empty
                      title="尚未绑定场景"
                      description="先从模拟场景库添加场景，再选择指定提示词。"
                    >
                      <Button onClick={() => setTab("scenarios")}>
                        选择模拟场景
                      </Button>
                    </Empty>
                  ) : (
                    workspace.bindings.map((binding, i) => {
                      const scenario = library.scenarios.find(
                        (s) => s.id === binding.scenario_id,
                      );
                      return (
                        <article className="composer-binding" key={binding.id}>
                          <div className="composer-section-line">
                            <div className="actions">
                              <Badge>{i + 1}</Badge>
                              <strong>
                                {scenario?.name || "场景已不存在"}
                              </strong>
                              <label className="checkbox-label">
                                <input
                                  type="checkbox"
                                  checked={binding.enabled !== false}
                                  onChange={(e) =>
                                    editBinding(binding.id, {
                                      enabled: e.target.checked,
                                    })
                                  }
                                />
                                启用
                              </label>
                            </div>
                            <div className="actions">
                              <Button
                                variant="ghost"
                                disabled={i === 0}
                                onClick={() => {
                                  const list = [...workspace.bindings];
                                  [list[i - 1], list[i]] = [
                                    list[i],
                                    list[i - 1],
                                  ];
                                  patchWorkspace({ bindings: list });
                                }}
                              >
                                上移
                              </Button>
                              <Button
                                icon={Trash2}
                                variant="ghost danger-text"
                                onClick={() =>
                                  patchWorkspace({
                                    bindings: workspace.bindings.filter(
                                      (b) => b.id !== binding.id,
                                    ),
                                  })
                                }
                              >
                                移除实例
                              </Button>
                            </div>
                          </div>
                          <Field
                            label={`实例 ${i + 1} 所属站点`}
                            hint="接口映射为站内路径；子站点会自动加上访问前缀。"
                          >
                            <Select
                              aria-label={`实例 ${i + 1} 所属站点`}
                              value={binding.site_node_id || "root"}
                              onChange={(e) =>
                                editBinding(binding.id, {
                                  site_node_id: e.target.value,
                                })
                              }
                            >
                              {workspaceSiteNodes(workspace).map((n) => (
                                <option key={n.id} value={n.id}>
                                  {n.name} · {n.mount_path}
                                </option>
                              ))}
                            </Select>
                          </Field>
                          <Field
                            label={`实例 ${i + 1} 默认提示词`}
                            hint="未单独指定的接口继承默认方案。已发布接口换绑后，保存工作区即可立即生效，包括已有会话。新增接口、路径和场景也会在保存后同步。"
                          >
                            <Select
                              value={binding.profile_id}
                              onChange={(e) =>
                                editBinding(binding.id, {
                                  profile_id: e.target.value,
                                })
                              }
                            >
                              <option value="">不设默认（逐接口指定）</option>
                              {data.profiles.map((p) => (
                                <option key={p.id} value={p.id}>
                                  {p.name} · v{p.version}
                                </option>
                              ))}
                            </Select>
                          </Field>
                          {scenario?.rules.map((r) => (
                            <div className="composer-path-binding" key={r.id}>
                              <div className="composer-rule-label">
                                <span>
                                  <Badge>{r.method}</Badge> {r.name}
                                </span>
                                {r.conditions?.map((condition, index) => (
                                  <small key={index}>
                                    {conditionLabel(condition)}
                                  </small>
                                ))}
                              </div>
                              <input
                                aria-label={`${scenario.name} ${r.name} 映射路径`}
                                value={binding.paths?.[r.id] ?? r.path}
                                onChange={(e) => {
                                  const paths = { ...binding.paths };
                                  if (e.target.value === r.path)
                                    delete paths[r.id];
                                  else paths[r.id] = e.target.value;
                                  editBinding(binding.id, { paths });
                                }}
                                onBlur={(e) => {
                                  if (!e.target.value) {
                                    const paths = { ...binding.paths };
                                    delete paths[r.id];
                                    editBinding(binding.id, { paths });
                                  }
                                }}
                              />
                              <div className="composer-rule-profile">
                                {r.delivery_required ? (
                                  <Field
                                    label={`实例 ${i + 1} · ${r.name || r.id} 提示词`}
                                  >
                                    <Select
                                      value={
                                        binding.rule_profiles?.[r.id] || ""
                                      }
                                      onChange={(e) => {
                                        const rule_profiles = {
                                          ...binding.rule_profiles,
                                        };
                                        if (e.target.value)
                                          rule_profiles[r.id] = e.target.value;
                                        else delete rule_profiles[r.id];
                                        editBinding(binding.id, {
                                          rule_profiles,
                                        });
                                      }}
                                    >
                                      <option value="">
                                        {binding.profile_id
                                          ? `继承默认 · ${data.profiles.find((p) => p.id === binding.profile_id)?.name || "所选方案"}`
                                          : "继承默认（尚未设置）"}
                                      </option>
                                      {data.profiles.map((p) => (
                                        <option key={p.id} value={p.id}>
                                          {p.name} · v{p.version}
                                        </option>
                                      ))}
                                    </Select>
                                  </Field>
                                ) : (
                                  <small className="muted">
                                    普通响应，不交付提示词。需要交付时，在模拟场景的响应正文中加入
                                    {" {{prompt}} "}，编辑器会自动启用交付。
                                  </small>
                                )}
                              </div>
                              <div className="composer-path-actions">
                                <CopyButton
                                  value={binding.paths?.[r.id] || r.path}
                                  label="复制路径"
                                  aria-label="复制路径"
                                  notify={notify}
                                />
                                <CopyButton
                                  value={previewRequestText({
                                    ...r,
                                    path: binding.paths?.[r.id] || r.path,
                                  })}
                                  label="复制示例请求"
                                  aria-label="复制示例请求"
                                  notify={notify}
                                />
                                <Button
                                  icon={Play}
                                  variant="ghost"
                                  onClick={() => {
                                    setPreviewTarget({
                                      bindingId: binding.id,
                                      ruleId: r.id,
                                    });
                                    setTab("preview");
                                  }}
                                >
                                  预演
                                </Button>
                              </div>
                            </div>
                          ))}
                        </article>
                      );
                    })
                  )}
                  <Callout>
                    路径可直接编辑和选中复制；留空后恢复场景原路径。「复制路径」复制实际绑定路径，「复制示例请求」同时带上方法和示例参数。绑定通过
                    HTTP
                    路由生效，与前端框架及页面类型无关。已发布实例换绑方案后保存工作区，已有会话也会同步新绑定；路径和场景改动保存后也会同步。
                  </Callout>
                </Panel>
              </div>
            )}
            <div
              hidden={tab !== "preview"}
              className="workspace-config-view"
              id="workspace-view-preview"
              role="tabpanel"
              aria-labelledby="workspace-tab-preview"
            >
              <RequestPreview
                key={workspace.id}
                workspace={workspace}
                scenarios={library.scenarios}
                profiles={data.profiles}
                site={site}
                target={previewTarget}
                onUseScenario={(item, profileId) => {
                  const binding = item.binding
                    ? workspace.bindings.find((b) => b.id === item.binding.id)
                    : {
                        id: uid("binding"),
                        scenario_id: item.scenario.id,
                        paths: {},
                      };
                  if (!binding) return;
                  const updated = {
                    ...binding,
                    profile_id: profileId,
                    enabled: true,
                  };
                  patchWorkspace({
                    bindings: item.binding
                      ? workspace.bindings.map((b) =>
                          b.id === binding.id ? updated : b,
                        )
                      : [...workspace.bindings, updated],
                  });
                  setPreviewTarget({
                    bindingId: binding.id,
                    ruleId: item.rule.id,
                  });
                  notify(
                    "场景接口已加入当前草稿并填入请求；预演后可保存工作区",
                  );
                }}
                onNavigate={setTab}
              />
            </div>
            {tab === "publish" && workspace.deployment_id && (
              <Panel
                id="workspace-view-publish"
                role="tabpanel"
                aria-labelledby="workspace-tab-publish"
                title="发布与测试入口"
                className="composer-content"
              >
                <div className="composer-section-line">
                  <p>
                    首次发布并绑定端口后，保存站点、场景或提示词绑定即可自动更新。已有会话的下一次请求也会使用新内容，历史交付记录保留。
                  </p>
                  <Button
                    icon={Send}
                    disabled={publicationPaused}
                    onClick={() =>
                      setPortDeployment(
                        published || {
                          id: workspace.deployment_id,
                          name: workspace.name,
                          workspace_id: workspace.id,
                        },
                      )
                    }
                  >
                    绑定测试端口
                  </Button>
                </div>
                {publicationPaused && (
                  <p className="muted">恢复发布后，可绑定并启用测试端口。</p>
                )}
                {listeners.length ? (
                  listeners.map((l) => (
                    <div className="composer-published-link" key={l.id}>
                      <Badge
                        tone={
                          publicationPaused
                            ? "amber"
                            : l.status === "running"
                              ? "green"
                              : ""
                        }
                      >
                        {publicationPaused
                          ? "发布已暂停"
                          : l.status === "running"
                            ? "运行中"
                            : "未运行"}
                      </Badge>
                      <Badge>{siteNodeName(workspace, l.site_node_id)}</Badge>
                      <code>{l.public_url}/</code>
                      <CopyButton value={`${l.public_url}/`} notify={notify} />
                      <a
                        className="button"
                        href={`${l.public_url}/`}
                        target="_blank"
                        rel="noreferrer"
                      >
                        <ExternalLink size={15} />
                        打开站点
                      </a>
                      <span className="muted">回传</span>
                      <code>
                        {l.public_url}
                        {workspace.callback_path || "/collect"}
                      </code>
                      <CopyButton
                        value={`${l.public_url}${workspace.callback_path || "/collect"}`}
                        notify={notify}
                      />
                    </div>
                  ))
                ) : (
                  <p className="muted">
                    尚未绑定端口。绑定并启用后，才会产生可访问的测试入口。
                  </p>
                )}
              </Panel>
            )}
            <div hidden={tab !== "publish"}>
              <CollectValidationSettings
                key={`validation-${workspace.id}`}
                workspace={workspace}
                onSaved={async (saved) => {
                  setWorkspace((current) =>
                    current?.id === saved.id
                      ? {
                          ...current,
                          collect_validation: saved.collect_validation,
                          version: saved.version,
                          updated_at: saved.updated_at,
                          publication_revision: saved.publication_revision,
                          published_version: saved.published_version,
                          published_at: saved.published_at,
                        }
                      : current,
                  );
                  await Promise.all([reload(), refresh()]);
                }}
              />
            </div>
            {tab === "publish" && (
              <CollectResponseSettings
                key={workspace.id}
                rejected
                workspace={workspace}
                onSaved={async (saved) => {
                  setWorkspace((current) =>
                    current?.id === saved.id
                      ? {
                          ...current,
                          collect_rejected_response:
                            saved.collect_rejected_response,
                          version: saved.version,
                          updated_at: saved.updated_at,
                          publication_revision: saved.publication_revision,
                          published_version: saved.published_version,
                          published_at: saved.published_at,
                        }
                      : current,
                  );
                  await Promise.all([reload(), refresh()]);
                }}
              />
            )}
            {tab === "publish" && !workspace.deployment_id && (
              <div
                className="workspace-config-view"
                id="workspace-view-publish"
                role="tabpanel"
                aria-labelledby="workspace-tab-publish"
              >
                <Empty
                  icon={Send}
                  title="尚未发布"
                  description={
                    site
                      ? "站点已准备好，可先预演请求，再点击右上角发布工作区。"
                      : "先选择站点素材，再配置模拟场景和提示词。"
                  }
                >
                  <Button onClick={() => setTab(site ? "preview" : "site")}>
                    {site ? "前往请求预演" : "选择站点素材"}
                  </Button>
                </Empty>
              </div>
            )}
          </div>
        </div>
      )}
      {route.id === "new" && !workspace && (
        <WorkspaceIdentityDialog
          busy={busy === "create"}
          onClose={() => navigate("composer")}
          onSave={async (identity) => {
            setBusy("create");
            try {
              const saved = await api("/workspaces", "POST", {
                ...identity,
                site_id: "",
                bindings: [],
              });
              await Promise.all([reload(), refresh()]);
              if (!alive.current) return;
              routeLoaded.current = saved.id;
              setWorkspace(saved);
              setTab("assistant");
              navigate("composer", saved.id);
              notify("工作区已创建并保存为草稿");
            } finally {
              setBusy("");
            }
          }}
        />
      )}
      {renamingWorkspace && (
        <WorkspaceIdentityDialog
          workspace={renamingWorkspace}
          renameOnly
          busy={!!busy}
          onClose={() => setRenamingWorkspace(null)}
          onSave={async ({ name }) => {
            setBusy("rename");
            try {
              const saved = await api(
                `/workspaces/${renamingWorkspace.id}/rename`,
                "POST",
                {
                  name,
                  version: renamingWorkspace.version,
                },
              );
              // Preserve unsaved site/binding edits while advancing their base version.
              setWorkspace((current) =>
                current?.id === saved.id
                  ? {
                      ...current,
                      name: saved.name,
                      version: saved.version,
                      updated_at: saved.updated_at,
                    }
                  : current,
              );
              await Promise.all([reload(), refresh()]);
              setRenamingWorkspace(null);
              notify("工作区已重命名");
            } finally {
              setBusy("");
            }
          }}
        />
      )}
      {siteLibraryOpen && (
        <SiteLibrary
          sites={library.sites}
          workspaces={library.workspaces}
          selectedSiteId={workspace?.site_id}
          onClose={() => setSiteLibraryOpen(false)}
          onDelete={async (target) => {
            await api(`/sites/${target.id}`, "DELETE");
            setWorkspace((current) =>
              current?.site_id === target.id
                ? { ...current, site_id: "" }
                : current,
            );
            await Promise.all([reload(), refresh()]);
            notify("站点素材已删除，发布快照和历史会话已保留");
          }}
        />
      )}
      {identityOpen && workspace && (
        <WorkspaceIdentityDialog
          workspace={workspace}
          busy={!!busy}
          onClose={() => setIdentityOpen(false)}
          onSave={async (identity) => {
            setBusy("save");
            try {
              await saveWorkspace({ ...workspace, ...identity });
              setIdentityOpen(false);
              notify("工作区设置已保存");
            } finally {
              setBusy("");
            }
          }}
          actions={
            <>
              <Button
                type="button"
                disabled={!!busy}
                onClick={() => {
                  setIdentityOpen(false);
                  setTab("publish");
                }}
              >
                令牌失效响应
              </Button>
              <Button
                type="button"
                icon={Copy}
                disabled={!!busy}
                onClick={() => {
                  setIdentityOpen(false);
                  copyWorkspace(workspace);
                }}
              >
                复制工作区
              </Button>
              {workspace.deployment_id && (
                <Button
                  type="button"
                  icon={publicationPaused ? Play : Pause}
                  disabled={!!busy}
                  onClick={() => setPublication(workspace, publicationPaused)}
                >
                  {publicationPaused ? "恢复发布" : "暂停发布"}
                </Button>
              )}
              <Button
                type="button"
                icon={Trash2}
                variant="ghost danger-text"
                disabled={!!busy}
                onClick={() => {
                  setIdentityOpen(false);
                  setDeletingWorkspace(workspace);
                }}
              >
                删除工作区
              </Button>
            </>
          }
        />
      )}
      {pendingLeave && (
        <Confirm
          title="离开工作区"
          description="当前有未保存的更改。离开后这些更改会被放弃，已保存的工作区和对话会保留。"
          confirmLabel="放弃更改并离开"
          onClose={() => setPendingLeave(null)}
          onConfirm={() => {
            const proceed = pendingLeave;
            setPendingLeave(null);
            proceed();
          }}
        />
      )}
      {siteEditor && (
        <SiteEditor
          initial={siteEditor}
          onClose={() => setSiteEditor(null)}
          onSave={async (saved) => {
            await reload();
            const selected =
              !siteEditor.id || workspace.site_id === siteEditor.id;
            if (selected) patchWorkspace({ site_id: saved.id });
            setSiteEditor(null);
            notify(
              selected
                ? "站点素材已保存并选入工作区，已发布引用同步更新"
                : "站点素材已保存，已发布引用同步更新",
            );
          }}
        />
      )}
      {deletingScenario && (
        <ScenarioDeleteDialog
          scenario={deletingScenario}
          workspaces={library.workspaces}
          selected={workspace?.bindings?.some(
            (binding) => binding.scenario_id === deletingScenario.id,
          )}
          onClose={() => setDeletingScenario(null)}
          onDelete={async (target) => {
            try {
              await api(`/scenarios/${target.id}`, "DELETE");
            } catch (e) {
              await reload();
              throw e;
            }
            setWorkspace((current) =>
              current
                ? {
                    ...current,
                    bindings: current.bindings.filter(
                      (binding) => binding.scenario_id !== target.id,
                    ),
                  }
                : current,
            );
            await Promise.all([reload(), refresh()]);
            notify("模拟场景已删除，发布快照和历史记录已保留");
          }}
        />
      )}
      {scenarioEditor && (
        <ScenarioEditor
          initial={scenarioEditor}
          onClose={() => setScenarioEditor(null)}
          onSave={async (saved) => {
            await reload();
            setScenarioEditor(null);
            if (!scenarioEditor.id) addBinding(saved);
            notify("模拟场景已保存，已同步到引用它的已发布端口");
          }}
        />
      )}
      {settingsOpen && (
        <GenerationSettings
          onClose={() => setSettingsOpen(false)}
          onSaved={(value) => {
            setModel(value);
            setModelError("");
          }}
        />
      )}
      {portDeployment && (
        <PublishPort
          deployment={portDeployment}
          onClose={() => setPortDeployment(null)}
        />
      )}
      {deletingWorkspace && (
        <Confirm
          title="删除工作区"
          description={`删除「${deletingWorkspace.name}」及其当前发布部署？关联端口将暂停并解绑，站点素材、场景、提示词和历史会话保留。`}
          loading={busy === "delete"}
          onClose={() => !busy && setDeletingWorkspace(null)}
          onConfirm={() =>
            run("delete", async () => {
              await api(`/workspaces/${deletingWorkspace.id}`, "DELETE");
              if (!alive.current) return;
              // Deletion was already confirmed. There is no saved workspace
              // left to return to, so do not ask to discard its draft again.
              guardNavigation(null);
              setDeletingWorkspace(null);
              setWorkspace(null);
              routeLoaded.current = null;
              navigate("composer");
              await Promise.all([reload(), refresh()]);
              notify("工作区已删除，关联端口已暂停解绑，历史会话保留");
            })
          }
        />
      )}
    </>
  );
}
