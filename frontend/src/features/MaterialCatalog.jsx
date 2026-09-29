import ListPagination, { useListPage } from "../components/ListPagination";
import React, { Fragment, useId, useState } from "react";
import {
  ChevronRight,
  Code2,
  Copy,
  Eye,
  FileCode2,
  Layers,
  Plus,
  Search,
  Trash2,
} from "lucide-react";
import { api, formatTime } from "../api";
import { Badge, Button, CopyButton, Empty } from "../components/UI";
import Select from "../components/Select";
import { conditionLabel } from "./preview-requests";
import "./material-catalog.css";

function SiteDetails({ site, notify }) {
  const [path, setPath] = useState(site.entry);
  const file = site.files.find((item) => item.path === path) || site.files[0];
  return (
    <>
      <p className="material-description">
        首页：<code>{site.entry}</code> ·{" "}
        {site.spa ? "支持 SPA 路由" : "按文件路径访问"}
      </p>
      <div className="material-file-viewer">
        <nav aria-label={`${site.name}的文件`}>
          {site.files.map((item) => (
            <button
              key={item.path}
              aria-current={file?.path === item.path ? "true" : undefined}
              onClick={() => setPath(item.path)}
            >
              <FileCode2 size={15} />
              <span>{item.path}</span>
            </button>
          ))}
        </nav>
        {file && (
          <section aria-label="文件内容">
            <div className="material-detail-heading">
              <code>{file.path}</code>
              {file.encoding !== "base64" && file.encoding !== "hosted" && (
                <CopyButton
                  value={file.content}
                  label="复制文件内容"
                  notify={notify}
                />
              )}
            </div>
            {file.encoding === "base64" || file.encoding === "hosted" ? (
              <p className="muted">
                二进制或托管资源，可在站点外观或 HTTP 下载链接中查看。
              </p>
            ) : (
              <pre className="material-source">{file.content}</pre>
            )}
          </section>
        )}
      </div>
    </>
  );
}

function ResponseDetails({ response, title, notify }) {
  if (!response) return null;
  return (
    <section className="material-response" aria-label={title}>
      <div className="material-detail-heading">
        <strong>{title}</strong>
        <Badge>HTTP {response.status}</Badge>
        <span className="muted">
          {response.content_type || response.format}
        </span>
        <CopyButton
          value={response.body || ""}
          label={`复制${title}`}
          notify={notify}
        />
      </div>
      {Object.keys(response.headers || {}).length > 0 && (
        <dl className="material-response-headers">
          {Object.entries(response.headers).map(([key, value]) => (
            <div key={key}>
              <dt>{key}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      )}
      <pre className="material-source">{response.body || "（空正文）"}</pre>
      {response.prompt_prefix && (
        <p className="muted">
          提示词行前缀：<code>{response.prompt_prefix}</code>
        </p>
      )}
    </section>
  );
}

function ScenarioDetails({ scenario, notify }) {
  return (
    <>
      {scenario.description && (
        <p className="material-description">{scenario.description}</p>
      )}
      <div className="material-rules">
        {scenario.rules.map((rule, index) => (
          <details className="material-rule" key={rule.id}>
            <summary>
              <ChevronRight size={15} className="material-chevron" />
              <Badge>{rule.method}</Badge>
              <code>{rule.path}</code>
              <span className="material-rule-name">
                {rule.name || `规则 ${index + 1}`}
              </span>
              <span className="material-rule-delivery">
                {rule.delivery_required ? "交付提示词" : "普通响应"}
              </span>
              <Badge>HTTP {rule.response.status}</Badge>
            </summary>
            <div className="material-rule-detail">
              <div className="material-detail-heading">
                <strong>命中条件</strong>
                <CopyButton
                  value={`${rule.method} ${rule.path}`}
                  label="复制接口路径"
                  notify={notify}
                />
              </div>
              {rule.conditions?.length ? (
                <ul className="material-conditions">
                  {rule.conditions.map((condition, i) => (
                    <li key={i}>{conditionLabel(condition)}</li>
                  ))}
                </ul>
              ) : (
                <p className="muted">
                  无附加条件，匹配此方法与路径的所有请求。
                </p>
              )}
              <ResponseDetails
                response={rule.response}
                title="命中响应"
                notify={notify}
              />
              {rule.delivery_required && (
                <p className="muted">
                  提示词由工作区的场景实例或接口绑定指定，实际返回可在“请求预演”中查看。
                </p>
              )}
              <ResponseDetails
                response={rule.fallback}
                title="条件未命中响应"
                notify={notify}
              />
            </div>
          </details>
        ))}
      </div>
    </>
  );
}

export default function MaterialCatalog({
  kind,
  items,
  workspace,
  workspaces,
  onSelect,
  onEdit,
  onCopy,
  onDelete,
  onAdd,
  notify,
}) {
  const siteMode = kind === "site";
  const label = siteMode ? "站点素材" : "模拟场景";
  const Icon = siteMode ? FileCode2 : Layers;
  const prefix = useId();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("all");
  const [expanded, setExpanded] = useState("");
  const [preview, setPreview] = useState(null);
  const [previewing, setPreviewing] = useState("");
  const [previewError, setPreviewError] = useState(null);
  const selectedCount = (item) =>
    siteMode
      ? Number(workspace.site_id === item.id)
      : (workspace.bindings || []).filter(
          (binding) => binding.scenario_id === item.id,
        ).length;
  const normalized = query.trim().toLocaleLowerCase();
  const visible = items.filter((item) => {
    const selected = selectedCount(item) > 0;
    const terms = siteMode
      ? item.files.map((file) => file.path)
      : item.rules.flatMap((rule) => [
          rule.name,
          rule.method,
          rule.path,
          ...(rule.conditions || []).map(conditionLabel),
        ]);
    return (
      (filter === "all" || (filter === "used" ? selected : !selected)) &&
      [item.name, item.description, ...terms]
        .join(" ")
        .toLocaleLowerCase()
        .includes(normalized)
    );
  });
  const toggle = (id) => setExpanded((current) => (current === id ? "" : id));
  const showPreview = async (site) => {
    setExpanded(site.id);
    setPreview(null);
    setPreviewing(site.id);
    setPreviewError(null);
    try {
      const result = await api("/sites/preview", "POST", { site });
      setPreview({ id: site.id, version: site.version, html: result.html });
    } catch (error) {
      setPreviewError({ id: site.id, message: error.message });
    } finally {
      setPreviewing("");
    }
  };
  const pagination = useListPage(
    visible,
    `${kind}:${workspace?.id}:${query}:${filter}`,
  );
  return (
    <div className="material-catalog" aria-label={`${label}列表`}>
      <div className="material-toolbar">
        <label className="material-search">
          <Search size={17} />
          <input
            aria-label={`搜索${label}`}
            placeholder={
              siteMode ? "搜索名称、文件路径…" : "搜索名称、接口、条件…"
            }
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <Select
          aria-label={`${label}选用状态`}
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
        >
          <option value="all">全部状态</option>
          <option value="used">{siteMode ? "当前选用" : "已加入工作区"}</option>
          <option value="unused">{siteMode ? "未选用" : "未加入工作区"}</option>
        </Select>
        <span className="material-result-count">
          {visible.length} / {items.length} {siteMode ? "个素材" : "个场景"}
        </span>
      </div>
      <div className="material-table-wrap">
        <table className="material-table" aria-label={label}>
          <thead>
            <tr>
              <th scope="col">{siteMode ? "素材名称" : "场景名称"}</th>
              <th scope="col">{siteMode ? "文件" : "接口"}</th>
              <th scope="col">选用状态</th>
              <th scope="col">更新时间</th>
              <th scope="col">操作</th>
            </tr>
          </thead>
          <tbody>
            {pagination.items.map((item) => {
              const count = selectedCount(item);
              const open = expanded === item.id;
              const detailID = `${prefix}-${item.id}`;
              const refs = workspaces.filter((target) =>
                siteMode
                  ? target.site_id === item.id ||
                    target.site_mounts?.some((m) => m.site_id === item.id)
                  : target.bindings?.some(
                      (binding) => binding.scenario_id === item.id,
                    ),
              );
              return (
                <Fragment key={item.id}>
                  <tr
                    className={`material-row material-${kind}-row ${open ? "is-expanded" : ""}`}
                    data-material-id={item.id}
                    data-selected={count > 0}
                    onClick={(event) => {
                      if (!event.target.closest("button, a, input, select"))
                        toggle(item.id);
                    }}
                  >
                    <td className="material-name-cell">
                      <button
                        className="material-row-toggle"
                        aria-label={`${open ? "收起" : "查看"}${label} ${item.name}`}
                        aria-expanded={open}
                        aria-controls={detailID}
                        onClick={() => toggle(item.id)}
                      >
                        <ChevronRight size={16} className="material-chevron" />
                        <span className="material-title">
                          <span>
                            <strong>{item.name}</strong>
                            <Badge>v{item.version}</Badge>
                          </span>
                          <small>
                            {siteMode
                              ? `首页 ${item.entry}`
                              : item.description || "自定义请求条件与响应"}
                          </small>
                        </span>
                      </button>
                    </td>
                    <td data-label={siteMode ? "文件" : "接口"}>
                      {siteMode ? item.files.length : item.rules.length}{" "}
                      {siteMode ? "个文件" : "条接口"}
                    </td>
                    <td data-label="状态">
                      <span
                        className={`material-use-state ${count ? "is-used" : ""}`}
                      >
                        <i />
                        {count
                          ? siteMode
                            ? "当前选用"
                            : `已加入 · ${count} 个实例`
                          : siteMode
                            ? "未选用"
                            : "未加入"}
                      </span>
                    </td>
                    <td className="material-time" data-label="更新">
                      <time dateTime={item.updated_at}>
                        {item.updated_at ? formatTime(item.updated_at) : "—"}
                      </time>
                    </td>
                    <td className="material-actions-cell">
                      <div className="material-row-actions">
                        <Button
                          variant="ghost"
                          icon={Code2}
                          onClick={() => onEdit(item)}
                        >
                          {siteMode ? "编辑文件" : "编辑"}
                        </Button>
                        <Button
                          variant="ghost"
                          icon={Copy}
                          aria-label={`${siteMode ? "复制素材" : "复制场景"} ${item.name}`}
                          title={siteMode ? "复制素材" : "复制场景"}
                          className="material-icon-action"
                          onClick={() => onCopy(item)}
                        >
                          <span className="sr-only">复制</span>
                        </Button>
                        {siteMode ? (
                          <>
                            <Button
                              variant="ghost"
                              icon={Eye}
                              className="material-icon-action"
                              aria-label={`预览外观 ${item.name}`}
                              title="预览外观"
                              disabled={!!previewing}
                              loading={previewing === item.id}
                              onClick={() => showPreview(item)}
                            >
                              <span className="sr-only">预览外观</span>
                            </Button>
                            <Button
                              variant="ghost"
                              onClick={() => onSelect(count ? null : item)}
                            >
                              {count ? "移除选用" : "选用"}
                            </Button>
                          </>
                        ) : (
                          <>
                            <Button
                              variant="ghost danger-text"
                              icon={Trash2}
                              className="material-icon-action"
                              aria-label={`删除场景 ${item.name}`}
                              title="删除场景"
                              onClick={() => onDelete(item)}
                            >
                              <span className="sr-only">删除</span>
                            </Button>
                            <Button
                              variant="ghost"
                              icon={Plus}
                              onClick={() => onAdd(item)}
                            >
                              {count ? "添加另一个实例" : "添加到工作区"}
                            </Button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                  {open && (
                    <tr className="material-expanded-row">
                      <td colSpan={5}>
                        <section
                          id={detailID}
                          className="material-details"
                          aria-label={`${item.name}详情`}
                        >
                          <div className="material-detail-heading">
                            <Icon size={17} />
                            <strong>{item.name}</strong>
                            <span className="muted">
                              {refs.length
                                ? `${refs.length} 个工作区引用`
                                : "尚无已保存的工作区引用"}
                            </span>
                          </div>
                          {refs.length > 0 && (
                            <p className="material-references">
                              引用工作区：
                              {refs.map((target) => target.name).join("、")}
                            </p>
                          )}
                          {siteMode ? (
                            <SiteDetails
                              key={`${item.id}:${item.version}`}
                              site={item}
                              notify={notify}
                            />
                          ) : (
                            <ScenarioDetails scenario={item} notify={notify} />
                          )}
                          {siteMode && previewing === item.id && (
                            <p role="status">正在生成预览…</p>
                          )}
                          {siteMode && previewError?.id === item.id && (
                            <p role="alert" className="composer-error">
                              {previewError.message}
                            </p>
                          )}
                          {siteMode &&
                            preview?.id === item.id &&
                            preview.version === item.version && (
                              <div className="material-preview">
                                <div className="material-detail-heading">
                                  <strong>外观预览</strong>
                                  <Button
                                    variant="ghost"
                                    onClick={() => setPreview(null)}
                                  >
                                    收起预览
                                  </Button>
                                </div>
                                <iframe
                                  className="composer-site-preview"
                                  sandbox=""
                                  title="当前站点预览"
                                  srcDoc={preview.html}
                                />
                              </div>
                            )}
                        </section>
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
            {!visible.length && (
              <tr>
                <td colSpan={5}>
                  <Empty
                    icon={Icon}
                    title={
                      items.length ? `没有匹配的${label}` : `还没有${label}`
                    }
                    description={
                      items.length
                        ? "试试其他关键词或选用状态。"
                        : "可新建、使用预设或通过 AI 助手创建。"
                    }
                  />
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <ListPagination {...pagination} label={`${label}分页`} />
    </div>
  );
}
