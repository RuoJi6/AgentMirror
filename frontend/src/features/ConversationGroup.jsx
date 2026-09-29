import React, { useEffect, useState } from "react";
import {
  Bot,
  ChevronRight,
  LoaderCircle,
  MessageSquare,
  Pencil,
} from "lucide-react";
import { api, formatTime } from "../api";
import { Button, Field, Modal } from "../components/UI";
import ListPagination from "../components/ListPagination";

export default function ConversationGroup({
  group,
  workspaceID,
  selectedID,
  busy,
  onSelect,
  onRenamed,
  statusNames,
}) {
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState(1);
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [editing, setEditing] = useState(null);
  const [title, setTitle] = useState("");
  const [saving, setSaving] = useState(false);
  const [renameError, setRenameError] = useState("");
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoading(true);
    api(
      `/generation/conversations?workspace_id=${encodeURIComponent(workspaceID)}&agent_id=${encodeURIComponent(group.id)}&page=${page}`,
    )
      .then((next) => {
        if (!cancelled) {
          setData(next);
          setPage(next.page);
          setError("");
        }
      })
      .catch((e) => {
        if (!cancelled) setError(e.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, workspaceID, group.id, group.revision, page, refresh]);
  const rename = async (event) => {
    event.preventDefault();
    if (saving || !title.trim()) return;
    setSaving(true);
    setRenameError("");
    try {
      const renamed = await api(
        `/generation/conversations/${editing.id}/rename`,
        "POST",
        { title: title.trim(), version: editing.title_version || 0 },
      );
      setData(
        (old) =>
          old && {
            ...old,
            items: old.items.map((item) =>
              item.id === renamed.id ? { ...item, ...renamed } : item,
            ),
          },
      );
      setEditing(null);
      setRefresh((value) => value + 1);
      // A failed background count refresh must not turn a saved title into an error.
      Promise.resolve(onRenamed()).catch(() => {});
    } catch (e) {
      setRenameError(e.message);
    } finally {
      setSaving(false);
    }
  };
  return (
    <section className="agent-conversation-group">
      <button
        className="agent-group-heading"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <ChevronRight size={14} className={open ? "expanded" : ""} />
        <Bot size={15} />
        <strong>{group.name}</strong>
        {group.running > 0 && (
          <span className="agent-group-running" aria-label="有会话运行中" />
        )}
        <small>{group.total}</small>
      </button>
      {open && (
        <div className="agent-group-content" aria-busy={loading}>
          {error && (
            <p className="agent-sidebar-empty" role="alert">
              {error}{" "}
              <button onClick={() => setRefresh((value) => value + 1)}>
                重试
              </button>
            </p>
          )}
          {!data && loading && <p className="agent-sidebar-empty">正在加载…</p>}
          {data && !data.items.length && !loading && (
            <p className="agent-sidebar-empty">暂无对话</p>
          )}
          {(data?.items || []).map((item) => (
            <div
              className={`agent-conversation-row ${item.id === selectedID ? "active" : ""}`}
              key={item.id}
            >
              <button
                className="agent-conversation"
                disabled={!!busy || (loading && data.page !== page)}
                data-conversation-id={item.id}
                data-status={item.status}
                aria-current={item.id === selectedID ? "true" : undefined}
                title={`${item.title}\n${formatTime(item.updated_at)} · ${item.turn_count || 1} 轮${item.provider?.model ? ` · ${item.provider.model}` : ""}`}
                onClick={() => onSelect(item.latest_job_id)}
              >
                {["queued", "running"].includes(item.status) ? (
                  <LoaderCircle size={15} className="spin" />
                ) : (
                  <MessageSquare size={15} />
                )}
                <span>
                  <strong>{item.title || "新对话"}</strong>
                  <small
                    className={`agent-conversation-status is-${item.status}`}
                  >
                    {statusNames[item.status] || "历史对话"} ·{" "}
                    {item.turn_count || 1} 轮
                  </small>
                </span>
              </button>
              <button
                className="agent-conversation-rename"
                aria-label={`重命名对话 ${item.title}`}
                title="重命名对话"
                onClick={() => {
                  setEditing(item);
                  setTitle(item.title);
                  setRenameError("");
                }}
              >
                <Pencil size={13} />
              </button>
            </div>
          ))}
          {data && (
            <ListPagination
              compact
              page={data.page}
              pages={data.pages}
              onChange={(next) => {
                setData(null);
                setPage(next);
              }}
              label={`${group.name}对话分页`}
            />
          )}
        </div>
      )}
      {editing && (
        <Modal
          title="重命名对话"
          onClose={() => !saving && setEditing(null)}
          footer={
            <>
              <Button disabled={saving} onClick={() => setEditing(null)}>
                取消
              </Button>
              <Button
                variant="primary"
                loading={saving}
                disabled={!title.trim()}
                type="submit"
                form={`rename-${group.id}`}
              >
                保存名称
              </Button>
            </>
          }
        >
          <form id={`rename-${group.id}`} onSubmit={rename}>
            <Field label="对话名称" hint="最多 80 个字">
              <input
                autoFocus
                maxLength={80}
                value={title}
                disabled={saving}
                onChange={(e) => setTitle(e.target.value)}
              />
            </Field>
            {renameError && (
              <p role="alert" className="composer-error">
                {renameError}
              </p>
            )}
          </form>
        </Modal>
      )}
    </section>
  );
}
