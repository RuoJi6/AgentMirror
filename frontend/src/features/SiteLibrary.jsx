import ListPagination, { useListPage } from "../components/ListPagination";
import React, { useState } from "react";
import { FileCode2, Search, Trash2 } from "lucide-react";
import { Badge, Button, Empty, Modal } from "../components/UI";

export default function SiteLibrary({
  sites,
  workspaces,
  selectedSiteId,
  onClose,
  onDelete,
}) {
  const [query, setQuery] = useState("");
  const [pending, setPending] = useState(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const references = (id) =>
    workspaces.filter((workspace) => workspace.site_id === id);
  const usedBy = pending ? references(pending.id) : [];
  const visible = sites.filter((site) =>
    `${site.name} ${site.entry}`
      .toLocaleLowerCase()
      .includes(query.trim().toLocaleLowerCase()),
  );
  const pagination = useListPage(visible, query);
  const cancel = () => {
    setPending(null);
    setError("");
  };
  const remove = async () => {
    setBusy(true);
    setError("");
    try {
      await onDelete(pending);
      setPending(null);
    } catch (error) {
      setError(error.message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      title={pending ? "删除站点素材" : "站点素材库"}
      wide={!pending}
      onClose={() => !busy && onClose()}
      footer={
        pending ? (
          <>
            <Button disabled={busy} onClick={cancel}>
              返回素材库
            </Button>
            <Button
              variant="danger"
              disabled={busy || usedBy.length > 0}
              loading={busy}
              onClick={remove}
            >
              确认删除
            </Button>
          </>
        ) : (
          <Button onClick={onClose}>关闭素材库</Button>
        )
      }
    >
      {pending ? (
        <div className="site-library-confirm">
          <p>
            删除「{pending.name}
            」及其当前文件？它将不再出现在素材库中，原有发布快照和历史交付记录会保留。
          </p>
          {usedBy.length > 0 ? (
            <div role="alert" className="composer-error">
              <p>此素材仍被以下工作区引用，暂时不能删除：</p>
              <ul>
                {usedBy.map((workspace) => (
                  <li key={workspace.id}>{workspace.name}</li>
                ))}
              </ul>
              <p>
                请先在这些工作区的“站点素材”中更换素材或点击“移除选用”，保存工作区后再删除。
              </p>
            </div>
          ) : pending.id === selectedSiteId ? (
            <p className="muted">当前未保存草稿中的站点选用也会移除。</p>
          ) : null}
          {error && (
            <p role="alert" className="composer-error">
              {error}
            </p>
          )}
        </div>
      ) : (
        <div className="site-library">
          <div className="workspace-list-tools">
            <label>
              <Search size={16} />
              <input
                aria-label="搜索站点素材"
                placeholder="搜索名称或首页文件…"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </label>
            <span>{visible.length} 个素材</span>
          </div>
          <p className="muted">
            这里管理所有工作区共用的站点素材。删除前会检查工作区引用。
          </p>
          <div className="site-library-list">
            {pagination.items.map((site) => {
              const refs = references(site.id);
              return (
                <article key={site.id} className="site-library-row">
                  <FileCode2 size={20} aria-hidden="true" />
                  <div className="site-library-info">
                    <strong>{site.name}</strong>
                    <p>
                      v{site.version} · {site.files.length} 个文件 ·{" "}
                      {site.entry}
                    </p>
                    {refs.length > 0 && (
                      <p>
                        引用：
                        {refs.map((workspace) => workspace.name).join("、")}
                      </p>
                    )}
                  </div>
                  <Badge>
                    {refs.length ? `${refs.length} 个工作区引用` : "未引用"}
                  </Badge>
                  <Button
                    icon={Trash2}
                    variant="ghost danger-text"
                    aria-label={`删除素材 ${site.name}`}
                    onClick={() => {
                      setPending(site);
                      setError("");
                    }}
                  >
                    删除
                  </Button>
                </article>
              );
            })}
            {!visible.length && (
              <Empty
                icon={FileCode2}
                title={sites.length ? "没有匹配的站点素材" : "还没有站点素材"}
                description={
                  sites.length
                    ? "试试其他名称或首页文件。"
                    : "可在工作区中导入、创建或通过 AI 生成。"
                }
              />
            )}
          </div>
          <ListPagination {...pagination} label="素材库分页" />
        </div>
      )}
    </Modal>
  );
}
