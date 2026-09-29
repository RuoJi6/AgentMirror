import React, { useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  Copy,
  FileCode2,
  Layers,
  MoreHorizontal,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Search,
  Settings2,
  Trash2,
} from "lucide-react";
import { Badge, Button, Empty, Field, Modal } from "../components/UI";
import { profileGroup, profileGroups } from "../profileGroups";

import Select from "../components/Select";
import { LIST_PAGE_SIZE } from "../components/ListPagination";

const workspacePageSize = LIST_PAGE_SIZE;
const workspaceViewKey = "agentmirror.workspace-list-view.v1";
function readWorkspaceView() {
  try {
    const value = JSON.parse(sessionStorage.getItem(workspaceViewKey));
    return {
      category: typeof value?.category === "string" ? value.category : "",
      profile: typeof value?.profile === "string" ? value.profile : "",
      query: typeof value?.query === "string" ? value.query : "",
      page:
        Number.isSafeInteger(value?.page) && value.page > 0 ? value.page : 1,
    };
  } catch {
    return { query: "", category: "", profile: "", page: 1 };
  }
}

function WorkspacePagination({ page, pages, onChange, bottom = false }) {
  return (
    <nav
      className="workspace-pagination"
      aria-label={bottom ? "工作区分页（底部）" : "工作区分页"}
    >
      <Button
        icon={ChevronLeft}
        disabled={page <= 1}
        onClick={() => onChange(page - 1)}
      >
        上一页
      </Button>
      <span aria-live={bottom ? undefined : "polite"}>
        第 {page} / {pages} 页
      </span>
      <Button
        icon={ChevronRight}
        disabled={page >= pages}
        onClick={() => onChange(page + 1)}
      >
        下一页
      </Button>
    </nav>
  );
}

function workspacePrompts(workspace, profilesByID, scenariosByID) {
  const profiles = new Set();
  const categories = new Set();
  for (const binding of workspace.bindings || []) {
    if (binding.enabled === false) continue;
    for (const rule of scenariosByID.get(binding.scenario_id)?.rules || []) {
      if (!rule.delivery_required) continue;
      const profile = profilesByID.get(
        binding.rule_profiles?.[rule.id] || binding.profile_id,
      );
      if (profile) {
        categories.add(profileGroup(profile));
        profiles.add(profile.id);
      }
    }
  }
  return { categories, profiles };
}

function WorkspacePromptCategories({ categories }) {
  return (
    <div
      className="workspace-prompt-categories"
      role="group"
      aria-label="提示词分类"
    >
      <span>提示词分类</span>
      {categories.size ? (
        Object.entries(profileGroups)
          .filter(([category]) => categories.has(category))
          .map(([category, label]) => <Badge key={category}>{label}</Badge>)
      ) : (
        <span>未使用提示词</span>
      )}
    </div>
  );
}

export function WorkspaceList({
  workspaces,
  sites,
  profiles,
  scenarios,
  busy,
  onCreate,
  onOpen,
  onCopy,
  onRename,
  onDelete,
  onPublication,
  onRefresh,
  onModels,
  onSites,
}) {
  const [view, setView] = useState(readWorkspaceView);
  const toolsRef = useRef(null);
  const profilesByID = useMemo(
    () => new Map(profiles.map((profile) => [profile.id, profile])),
    [profiles],
  );
  const scenariosByID = useMemo(
    () => new Map(scenarios.map((scenario) => [scenario.id, scenario])),
    [scenarios],
  );
  const { query, category, profile } = view;
  const promptsByWorkspace = useMemo(
    () =>
      new Map(
        workspaces.map((item) => [
          item.id,
          workspacePrompts(item, profilesByID, scenariosByID),
        ]),
      ),
    [workspaces, profilesByID, scenariosByID],
  );
  const filtered = workspaces.filter((item) => {
    const used = promptsByWorkspace.get(item.id);
    return (
      `${item.name} ${item.slug}`
        .toLocaleLowerCase()
        .includes(query.trim().toLocaleLowerCase()) &&
      (!category ||
        (category === "none"
          ? !used.categories.size
          : used.categories.has(category))) &&
      (!profile || used.profiles.has(profile))
    );
  });
  const pages = Math.max(1, Math.ceil(filtered.length / workspacePageSize));
  const page = Math.min(view.page, pages);
  const start = (page - 1) * workspacePageSize;
  const visible = filtered.slice(start, start + workspacePageSize);
  useEffect(() => {
    // Clamp after deletion/refresh, but retain a valid page when returning
    // from an editor. WorkspaceList only mounts after its data has loaded.
    if (view.page !== page) setView((old) => ({ ...old, page }));
    try {
      sessionStorage.setItem(
        workspaceViewKey,
        JSON.stringify({ query, category, profile, page }),
      );
    } catch {
      // Pagination also works when browser storage is unavailable.
    }
  }, [query, category, profile, page, view.page]);
  const changePage = (next) => {
    setView((old) => ({ ...old, page: Math.min(pages, Math.max(1, next)) }));
    toolsRef.current?.scrollIntoView({ block: "start" });
  };
  return (
    <div className="workspace-index">
      <header className="workspace-index-heading">
        <div>
          <h1>蜜罐工作区</h1>
          <p>每个工作区独立管理对话、站点、模拟场景和发布。</p>
        </div>
        <div className="actions">
          <Button icon={FileCode2} variant="ghost" onClick={onSites}>
            站点素材
          </Button>
          <Button icon={Settings2} variant="ghost" onClick={onModels}>
            生成模型设置
          </Button>
          <Button
            icon={RefreshCw}
            variant="ghost"
            disabled={busy}
            onClick={onRefresh}
          >
            刷新素材
          </Button>
          {workspaces.length > 0 && (
            <Button icon={Plus} variant="primary" onClick={onCreate}>
              新建工作区
            </Button>
          )}
        </div>
      </header>
      {!workspaces.length ? (
        <div className="workspace-index-empty">
          <Empty
            icon={Layers}
            title="还没有蜜罐工作区"
            description="先创建一个工作区，再与 AI 对话，或导入自己的站点和模拟场景。"
          >
            <Button variant="primary" icon={Plus} onClick={onCreate}>
              创建第一个工作区
            </Button>
          </Empty>
        </div>
      ) : (
        <>
          <div className="workspace-list-tools" ref={toolsRef}>
            <label>
              <Search size={16} />
              <input
                aria-label="搜索工作区"
                placeholder="搜索名称或部署标识…"
                value={query}
                onChange={(e) =>
                  setView((old) => ({ ...old, query: e.target.value, page: 1 }))
                }
              />
            </label>
            <Select
              aria-label="按提示词分类筛选工作区"
              value={category}
              onChange={(e) =>
                setView((old) => ({
                  ...old,
                  category: e.target.value,
                  page: 1,
                }))
              }
            >
              <option value="">全部提示词分类</option>
              {Object.entries(profileGroups).map(([id, name]) => (
                <option key={id} value={id}>
                  {name}
                </option>
              ))}
              <option value="none">未使用提示词</option>
            </Select>
            <Select
              aria-label="按提示词方案筛选工作区"
              value={profile}
              onChange={(e) =>
                setView((old) => ({ ...old, profile: e.target.value, page: 1 }))
              }
            >
              <option value="">全部提示词方案</option>
              {profiles.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </Select>
            <span>
              {filtered.length} 个工作区
              {filtered.length > 0 &&
                ` · 显示 ${start + 1}–${start + visible.length}`}
            </span>
            {filtered.length > workspacePageSize && (
              <WorkspacePagination
                page={page}
                pages={pages}
                onChange={changePage}
              />
            )}
          </div>
          <div className="workspace-rows">
            {visible.map((workspace) => (
              <article
                className="composer-workspace-card workspace-row"
                key={workspace.id}
              >
                <div className="workspace-row-symbol">
                  <Layers size={21} />
                </div>
                <div className="workspace-row-main">
                  <button
                    className="workspace-row-name"
                    onClick={() => onOpen(workspace)}
                  >
                    {workspace.name}
                  </button>
                  <p>
                    {sites.find((item) => item.id === workspace.site_id)
                      ?.name || "尚未选择站点"}{" "}
                    · {workspace.bindings?.length || 0} 个场景 ·{" "}
                    {workspace.slug}
                  </p>
                  <WorkspacePromptCategories
                    categories={promptsByWorkspace.get(workspace.id).categories}
                  />
                </div>
                <Badge
                  tone={
                    workspace.publication_status === "paused"
                      ? "amber"
                      : workspace.deployment_id
                        ? "green"
                        : ""
                  }
                >
                  {workspace.publication_status === "paused"
                    ? "发布已暂停"
                    : workspace.deployment_id
                      ? `已发布 v${workspace.published_version || workspace.version}`
                      : "草稿"}
                </Badge>
                <Button onClick={() => onOpen(workspace)}>打开工作区</Button>
                <details className="workspace-row-menu">
                  <summary aria-label={`更多操作 ${workspace.name}`}>
                    <MoreHorizontal size={18} />
                  </summary>
                  <div>
                    <Button
                      icon={Pencil}
                      disabled={busy}
                      onClick={(event) => {
                        event.currentTarget.closest("details").open = false;
                        onRename(workspace);
                      }}
                    >
                      重命名
                    </Button>
                    <Button
                      icon={Copy}
                      disabled={busy}
                      onClick={() => onCopy(workspace)}
                    >
                      复制工作区
                    </Button>
                    {workspace.deployment_id && (
                      <Button
                        icon={
                          workspace.publication_status === "paused"
                            ? Play
                            : Pause
                        }
                        disabled={busy}
                        onClick={() =>
                          onPublication(
                            workspace,
                            workspace.publication_status === "paused",
                          )
                        }
                      >
                        {workspace.publication_status === "paused"
                          ? "恢复发布"
                          : "暂停发布"}
                      </Button>
                    )}
                    <Button
                      icon={Trash2}
                      variant="ghost danger-text"
                      disabled={busy}
                      onClick={() => onDelete(workspace)}
                    >
                      删除工作区
                    </Button>
                  </div>
                </details>
              </article>
            ))}
            {!visible.length && (
              <p className="workspace-search-empty">
                没有匹配的工作区，试试其他名称或标识。
              </p>
            )}
          </div>
          {filtered.length > workspacePageSize && (
            <WorkspacePagination
              page={page}
              pages={pages}
              onChange={changePage}
              bottom
            />
          )}
        </>
      )}
    </div>
  );
}

export function WorkspaceIdentityDialog({
  workspace,
  busy,
  onClose,
  onSave,
  actions,
  renameOnly = false,
}) {
  const editing = !!workspace?.id;
  const [identity, setIdentity] = useState(() => ({
    name: workspace?.name || "",
    slug: workspace?.slug || `workspace-${Date.now().toString(36)}`,
    callback_path: workspace?.callback_path || "/collect",
    server_header: workspace?.server_header || "",
  }));
  const [error, setError] = useState("");
  const submit = async () => {
    setError("");
    try {
      if (!identity.name.trim()) throw new Error("请输入工作区名称。");
      if (!renameOnly && !/^[a-z0-9][a-z0-9-]*$/.test(identity.slug))
        throw new Error("部署标识请使用小写字母、数字和连字符。");
      await onSave({
        name: identity.name.trim(),
        ...(!renameOnly && {
          slug: identity.slug,
          callback_path: identity.callback_path.trim(),
          server_header: identity.server_header.trim(),
        }),
      });
    } catch (error) {
      setError(error.message);
    }
  };
  return (
    <Modal
      title={
        renameOnly ? "重命名工作区" : editing ? "工作区设置" : "新建工作区"
      }
      onClose={() => !busy && onClose()}
      footer={
        <>
          <Button disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={busy || !identity.name.trim()}
            onClick={submit}
          >
            {renameOnly ? "保存名称" : editing ? "保存设置" : "创建工作区"}
          </Button>
        </>
      }
    >
      <form
        className="workspace-identity-form"
        onSubmit={(event) => {
          event.preventDefault();
          if (!busy) submit();
        }}
      >
        <p className="muted">
          {renameOnly
            ? "只更新工作区名称，访问地址保持不变；当前未保存的配置可继续编辑。"
            : editing
              ? "设置工作区名称、部署标识、响应头与回传路径，已有素材和对话会保留。"
              : "创建后立即保存为草稿，再开始与 AI 对话或配置素材。"}
        </p>
        <Field label="工作区名称">
          <input
            autoFocus
            maxLength={80}
            value={identity.name}
            placeholder="例如：内部配置中心实验"
            onChange={(e) => setIdentity({ ...identity, name: e.target.value })}
          />
        </Field>
        {!renameOnly && (
          <Field
            label="部署标识"
            hint="小写字母、数字和连字符，可保留自动生成的标识。"
          >
            <input
              maxLength={60}
              value={identity.slug}
              onChange={(e) =>
                setIdentity({ ...identity, slug: e.target.value })
              }
            />
          </Field>
        )}
        {!renameOnly && (
          <Field
            label="回传路径"
            hint="完整地址使用当前蜜罐端口的公开地址。保存后同步；提示词中使用 {{callback_url}}。"
          >
            <input
              value={identity.callback_path}
              maxLength={256}
              placeholder="/collect"
              onChange={(e) =>
                setIdentity({ ...identity, callback_path: e.target.value })
              }
            />
          </Field>
        )}
        {!renameOnly && (
          <Field
            label="Server 响应头"
            hint="填写模拟 HTTP 服务的标识，例如 nginx 或 Apache；留空不发送。保存后同步所有绑定端口，接口单独配置的 Server 优先。"
          >
            <input
              maxLength={200}
              value={identity.server_header}
              placeholder="例如：nginx"
              onChange={(e) =>
                setIdentity({ ...identity, server_header: e.target.value })
              }
            />
          </Field>
        )}
        {error && (
          <p className="composer-error" role="alert">
            {error}
          </p>
        )}
        {actions && <div className="workspace-settings-actions">{actions}</div>}
      </form>
    </Modal>
  );
}

export function WorkspaceToolbar({
  workspace,
  dirty,
  busy,
  site,
  onBack,
  onSettings,
  onRename,
  onSave,
  onPublish,
}) {
  return (
    <header className="workspace-toolbar">
      <div className="workspace-toolbar-title">
        <Button
          icon={ArrowLeft}
          variant="ghost"
          aria-label="返回工作区列表"
          onClick={onBack}
        />
        <h1>{workspace.name}</h1>
        <Button
          icon={Pencil}
          variant="ghost"
          aria-label="重命名工作区"
          title="重命名工作区"
          disabled={!!busy}
          onClick={onRename}
        />
        <span className="workspace-save-state">
          {dirty
            ? "有未保存更改"
            : workspace.publication_status === "paused"
              ? `已保存 v${workspace.version} · 发布已暂停`
              : workspace.publication_status === "published"
                ? workspace.published_version === workspace.version
                  ? `已同步 v${workspace.version}`
                  : `已保存 v${workspace.version} · 待同步`
                : `草稿 v${workspace.version}`}
        </span>
      </div>
      <div className="actions">
        <Button icon={Settings2} onClick={onSettings}>
          工作区设置
        </Button>
        <Button
          disabled={!!busy || !dirty}
          loading={busy === "save"}
          onClick={onSave}
        >
          保存工作区
        </Button>
        <Button
          variant="primary"
          disabled={!!busy || !site}
          title={!site ? "选择站点并保存后即可发布" : undefined}
          loading={busy === "publish"}
          onClick={onPublish}
        >
          发布工作区
        </Button>
      </div>
    </header>
  );
}
