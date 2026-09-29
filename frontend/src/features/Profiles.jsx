import React, { useEffect, useId, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  Plus,
  Save,
  History,
  Trash2,
  Copy,
  ClipboardPaste,
  Check,
  Search,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";
import { useApp } from "../App";
import { api } from "../api";
import {
  Button,
  PageHeader,
  Field,
  Modal,
  Confirm,
  Badge,
  Callout,
  JsonView,
} from "../components/UI";
import { findProtocol, makeProtocol, resultPlaceholders } from "../protocol";
import Select from "../components/Select";
import ProfileHistory from "./ProfileHistory";
import {
  profileGroups,
  profileGroup,
  profileGroupHints,
} from "../profileGroups";
const tabs = [
  ["body", "提示词正文"],
  ["preview", "渲染预览"],
];
const blank = () => ({
  name: "新提示词方案",
  category: "command_execution",
  description: "",
  body: "",
  fields: [],
  commands: [],
});
function InsertButton({ description, children, ...props }) {
  const id = useId();
  const anchor = useRef(null);
  const [position, setPosition] = useState(null);
  useEffect(() => {
    const reposition = () => setPosition((current) => current && locate());
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    return () => {
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
    };
  }, []);
  const locate = () => {
    const rect = anchor.current.getBoundingClientRect();
    if (rect.bottom < 0 || rect.top > window.innerHeight) return null;
    const width = Math.min(300, window.innerWidth - 32);
    return {
      width,
      left: Math.max(16, Math.min(rect.left, window.innerWidth - width - 16)),
      ...(rect.top > 150
        ? { bottom: window.innerHeight - rect.top + 8 }
        : { top: rect.bottom + 8 }),
    };
  };
  const show = (event) => {
    anchor.current = event.currentTarget;
    setPosition(locate());
  };
  return (
    <>
      <Button
        {...props}
        aria-describedby={id}
        onMouseEnter={show}
        onMouseLeave={() => setPosition(null)}
        onFocus={show}
        onBlur={() => setPosition(null)}
        onKeyDown={(event) => {
          if (event.key === "Escape") setPosition(null);
        }}
      >
        {children}
      </Button>
      {createPortal(
        <span
          id={id}
          role="tooltip"
          className="placeholder-tooltip"
          hidden={!position}
          style={position || undefined}
        >
          {description}
        </span>,
        document.body,
      )}
    </>
  );
}
export default function Profiles() {
  const { data, route, navigate, mutate, notify } = useApp();
  const editor = useRef(null);
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const filtered = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    return data.profiles.filter(
      (profile) =>
        (!category || profileGroup(profile) === category) &&
        (!query ||
          [profile.name, profile.description, profile.body, profile.id].some(
            (value) =>
              String(value || "")
                .toLocaleLowerCase()
                .includes(query),
          )),
    );
  }, [data.profiles, category, search]);
  const pages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const currentPage = Math.min(page, pages);
  const visible = filtered.slice(
    (currentPage - 1) * pageSize,
    currentPage * pageSize,
  );
  useEffect(() => setPage((previous) => Math.min(previous, pages)), [pages]);
  const [draft, setDraft] = useState(null),
    [tab, setTab] = useState("body"),
    [busy, setBusy] = useState(false),
    [history, setHistory] = useState(null),
    [preview, setPreview] = useState(""),
    [deleting, setDeleting] = useState(false),
    [dirty, setDirty] = useState(false),
    [pending, setPending] = useState(null);
  useEffect(() => {
    const profile =
      route.id === "new"
        ? blank()
        : data.profiles.find((p) => p.id === route.id) ||
          data.profiles.find((p) => p.id === "receipt") ||
          data.profiles[0];
    setDraft(profile ? structuredClone(profile) : blank());
    setDirty(false);
    setPreview("");
    setTab("body");
  }, [route.id]);
  useEffect(() => {
    const handler = (e) => {
      if (dirty) {
        e.preventDefault();
        e.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [dirty]);
  const change = (k, v) => {
    setDraft({ ...draft, [k]: v });
    setDirty(true);
    setPreview("");
  };
  const choose = (id) => {
    if (dirty) setPending(id);
    else navigate("profiles", id);
  };
  const save = async () => {
    setBusy(true);
    const result = await mutate(
      "/profiles",
      "POST",
      draft,
      "已保存新版本，绑定此方案的后续请求立即生效",
    );
    setBusy(false);
    if (result) {
      setDraft(result);
      setDirty(false);
      navigate("profiles", result.id);
    }
  };
  const showPreview = async () => {
    setTab("preview");
    try {
      const r = await api(
        "/profiles/" + (draft.id || "draft") + "/preview",
        "POST",
        draft,
      );
      setPreview(r.instruction);
    } catch (e) {
      notify(e.message, "error");
    }
  };
  const body = draft?.body || "";
  const placeholders = useMemo(() => resultPlaceholders(body), [body]);
  const existingProtocol = useMemo(() => findProtocol(body), [body]);
  const insert = (text) => {
    const input = editor.current;
    const start = input?.selectionStart ?? body.length;
    const end = input?.selectionEnd ?? start;
    const updated = body.slice(0, start) + text + body.slice(end);
    if (updated.length > 50000)
      return notify("正文过长，请缩短后再插入（最多 50,000 字符）", "error");
    change("body", updated);
    requestAnimationFrame(() => {
      input?.focus();
      input?.setSelectionRange(start + text.length, start + text.length);
    });
  };
  const insertProtocol = () => {
    const found = findProtocol(body);
    if (found) {
      editor.current?.focus();
      editor.current?.setSelectionRange(found.start, found.end);
      notify("检测到已有请求协议或请求头，请在正文中查看和修改");
      return;
    }
    insert("\n\n" + makeProtocol(body) + "\n\n");
  };
  if (!draft) return null;
  return (
    <>
      <PageHeader
        title="提示词方案"
        description="编写提示词，用结果占位符标记回传内容，一键插入请求协议。"
      >
        <Button icon={Plus} variant="primary" onClick={() => choose("new")}>
          新建方案
        </Button>
      </PageHeader>
      <div className="editor-layout">
        <aside className="scheme-list panel">
          <div className="panel-header">
            <h2>方案库</h2>
            <Badge>{data.profiles.length}</Badge>
          </div>
          <div className="profile-library-filters">
            <div className="profile-search">
              <Search size={16} aria-hidden="true" />
              <input
                aria-label="搜索提示词方案"
                placeholder="搜索名称、说明或正文"
                maxLength={200}
                value={search}
                onChange={(event) => {
                  setSearch(event.target.value);
                  setPage(1);
                }}
              />
            </div>
            <Select
              aria-label="筛选方案分组"
              value={category}
              onChange={(event) => {
                setCategory(event.target.value);
                setPage(1);
              }}
            >
              <option value="">全部分组</option>
              {Object.entries(profileGroups).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </Select>
            <span className="profile-library-count">
              匹配 {filtered.length} / 共 {data.profiles.length} 个方案
            </span>
          </div>
          <div className="scheme-items">
            {visible.map((p) => (
              <button
                key={p.id}
                className={draft.id === p.id ? "selected" : ""}
                onClick={() => choose(p.id)}
              >
                <span>{p.name}</span>
                <Badge>
                  {profileGroups[profileGroup(p)]} · v{p.version}
                </Badge>
              </button>
            ))}
            {!filtered.length && (
              <div className="profile-library-empty">
                <p>{data.profiles.length ? "没有匹配的方案" : "方案库为空"}</p>
                {(search || category) && (
                  <Button
                    onClick={() => {
                      setSearch("");
                      setCategory("");
                      setPage(1);
                    }}
                  >
                    清除筛选
                  </Button>
                )}
              </div>
            )}
            {!draft.id && (
              <button className="selected">
                <span>新方案</span>
                <Badge>未保存</Badge>
              </button>
            )}
          </div>
          <div className="profile-library-pagination">
            <Select
              aria-label="每页方案数量"
              value={pageSize}
              onChange={(event) => {
                setPageSize(Number(event.target.value));
                setPage(1);
              }}
            >
              {[5, 10, 20, 50].map((size) => (
                <option key={size} value={size}>
                  {size} 条 / 页
                </option>
              ))}
            </Select>
            <div className="profile-page-controls">
              <span>
                {filtered.length ? (currentPage - 1) * pageSize + 1 : 0}–
                {Math.min(currentPage * pageSize, filtered.length)} /{" "}
                {filtered.length} 条
              </span>
              <Button
                icon={ChevronLeft}
                aria-label="上一页方案"
                disabled={currentPage === 1}
                onClick={() => setPage(currentPage - 1)}
              />
              <span>
                {currentPage} / {pages}
              </span>
              <Button
                icon={ChevronRight}
                aria-label="下一页方案"
                disabled={currentPage === pages}
                onClick={() => setPage(currentPage + 1)}
              />
            </div>
          </div>
        </aside>
        <section className="editor-panel panel">
          <div className="editor-head profile-editor-head">
            <h2>
              {draft.name}
              {dirty && <small>未保存</small>}
            </h2>
            <div className="actions" role="group" aria-label="方案操作">
              {draft.id && (
                <Button
                  icon={Copy}
                  onClick={() => {
                    const { id, version, ...copy } = draft;
                    setDraft({ ...copy, name: draft.name + " · 副本" });
                    setDirty(true);
                  }}
                >
                  另存为新方案
                </Button>
              )}
              {draft.id && (
                <Button
                  icon={History}
                  onClick={() => setHistory({ id: draft.id })}
                >
                  版本历史
                </Button>
              )}
              {draft.id && (
                <Button
                  icon={Trash2}
                  variant="ghost danger-text"
                  onClick={() => setDeleting(true)}
                >
                  删除方案
                </Button>
              )}
              <Button
                icon={Save}
                variant="accent"
                loading={busy}
                onClick={save}
              >
                保存新版本
              </Button>
            </div>
          </div>
          <div className="tabs">
            {tabs.map(([key, label]) => (
              <button
                key={key}
                className={tab === key ? "active" : ""}
                onClick={() =>
                  key === "preview" ? showPreview() : setTab(key)
                }
              >
                {label}
              </button>
            ))}
          </div>
          <div className="editor-content">
            <p className="muted">
              保存后，已部署入口和已有会话的下一次请求都会使用新版提示词，无需重新发布。历史交付内容保留原版本。
            </p>
            {tab === "body" && (
              <>
                <div className="form-grid">
                  <Field label="方案名称">
                    <input
                      value={draft.name}
                      onChange={(e) => change("name", e.target.value)}
                    />
                  </Field>
                  <Field
                    label="方案分组"
                    hint={profileGroupHints[profileGroup(draft)]}
                  >
                    <Select
                      value={profileGroup(draft)}
                      onChange={(event) =>
                        change("category", event.target.value)
                      }
                    >
                      {Object.entries(profileGroups).map(([value, label]) => (
                        <option key={value} value={value}>
                          {label}
                        </option>
                      ))}
                    </Select>
                  </Field>
                </div>
                <Field label="说明">
                  <input
                    value={draft.description}
                    onChange={(e) => change("description", e.target.value)}
                  />
                </Field>
                <Field
                  label="提示词正文"
                  hint="使用 {{result.system_time}}、{{result.output}} 等标记结果；会话变量由系统替换，结果占位符由被测客户端填写。"
                >
                  <textarea
                    ref={editor}
                    placeholder="例如：将任务执行结果填写为 {{result.output}}，再按回传协议提交。"
                    maxLength={50000}
                    className="prompt-editor"
                    spellCheck="false"
                    value={draft.body}
                    onChange={(e) => change("body", e.target.value)}
                  />
                </Field>
                <div className="protocol-toolbar">
                  <div>
                    <strong>
                      {existingProtocol
                        ? "已检测到请求协议或请求头"
                        : "自动生成回传协议"}
                    </strong>
                    <p>
                      在正文中放好光标后插入；已有协议时定位到原文，避免重复插入。
                    </p>
                  </div>
                  <Button
                    icon={existingProtocol ? Check : ClipboardPaste}
                    onClick={insertProtocol}
                  >
                    {existingProtocol ? "协议已存在 · 定位" : "插入回传协议"}
                  </Button>
                </div>
                <div className="placeholder-toolbar">
                  <strong>插入占位符</strong>
                  <p>在正文光标处插入；悬浮或聚焦按钮可查看用途。</p>
                  <div className="placeholder-actions">
                    <InsertButton
                      icon={Plus}
                      description="在光标处插入 {{result.output}}，标记希望客户端回传的结果。可将 output 改为 system_time 等英文名称。客户端需用实际内容替换它；此按钮不会执行命令或自动采集数据。"
                      onClick={() => insert("{{result.output}}")}
                    >
                      插入结果占位符
                    </InsertButton>
                    {[
                      [
                        "run_id",
                        "会话编号",
                        "插入 {{run_id}}。投放时由系统替换为当前会话编号，用于把回传内容关联到本次访问。",
                      ],
                      [
                        "callback_url",
                        "接收地址",
                        "插入 {{callback_url}}。使用当前蜜罐端口的公开地址与工作区回传路径，替换为接收 POST 的完整地址。",
                      ],
                      [
                        "token",
                        "关联令牌",
                        "插入 {{token}}。同一会话每次业务请求生成新令牌，回传只接受最新值，旧值立即失效。不要在场景条件中写死某次令牌。",
                      ],
                    ].map(([variable, label, description]) => (
                      <InsertButton
                        key={variable}
                        description={description}
                        onClick={() => insert(`{{${variable}}}`)}
                      >
                        {label}
                      </InsertButton>
                    ))}
                  </div>
                  <div className="result-placeholders">
                    <span>
                      {placeholders.length
                        ? `已识别 ${placeholders.length} 个结果占位符`
                        : "尚未添加结果占位符"}
                    </span>
                    {placeholders.slice(0, 12).map((name) => (
                      <code key={name}>{`{{${name}}}`}</code>
                    ))}
                    {placeholders.length > 12 && (
                      <span>另有 {placeholders.length - 12} 项</span>
                    )}
                  </div>
                </div>
              </>
            )}
            {tab === "preview" && (
              <>
                <div className="section-toolbar">
                  <div>
                    <h3>渲染后的提示词</h3>
                    <p>使用当前草稿；编号、令牌和地址均为预览值。</p>
                  </div>
                  <Button onClick={showPreview}>刷新预览</Button>
                </div>
                <JsonView value={preview || "正在生成预览…"} />
                <Callout>
                  实际回传地址由访问的蜜罐端口及工作区回传路径决定。结果占位符保留给客户端填写，不会在服务器上执行或求值。
                </Callout>
              </>
            )}
            {tab === "body" && (
              <Callout>服务仅保存与投放命令文本，不在服务端执行。</Callout>
            )}
          </div>
        </section>
      </div>
      {history && (
        <ProfileHistory
          key={history.id}
          profile={history}
          hasUnsaved={dirty}
          notify={notify}
          onClose={() => setHistory(null)}
          onRestore={async (version) => {
            const current = data.profiles.find((p) => p.id === version.id);
            if (!current) return notify("方案已不存在，请刷新后查看", "error");
            const result = await mutate(
              "/profiles",
              "POST",
              { ...version, version: current.version },
              "历史版本已恢复为新版本，后续请求立即生效",
            );
            if (result) {
              setDraft(result);
              setDirty(false);
              setHistory(null);
            }
          }}
        />
      )}
      {deleting && (
        <Confirm
          title="删除方案"
          description={`删除「${draft.name}」及其版本历史？已经生成的会话快照仍然保留。`}
          onClose={() => setDeleting(false)}
          onConfirm={async () => {
            const r = await mutate(
              "/profiles/" + draft.id,
              "DELETE",
              undefined,
              "提示词方案已删除",
            );
            if (r) {
              setDeleting(false);
              navigate("profiles");
            }
          }}
        />
      )}
      {pending && (
        <Modal
          title="还有未保存的修改"
          onClose={() => setPending(null)}
          footer={
            <>
              <Button onClick={() => setPending(null)}>继续编辑</Button>
              <Button
                variant="primary"
                onClick={() => {
                  navigate("profiles", pending);
                  setPending(null);
                }}
              >
                放弃修改并切换
              </Button>
            </>
          }
        >
          <p>切换方案会丢弃当前草稿；已保存版本不受影响。</p>
        </Modal>
      )}
    </>
  );
}
