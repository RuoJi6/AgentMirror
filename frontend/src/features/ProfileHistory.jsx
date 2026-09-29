import React, { useEffect, useMemo, useRef, useState } from "react";
import { Bot, History, RotateCcw, UserRound } from "lucide-react";
import { api, formatTime } from "../api";
import { Badge, Button, CopyButton, Field, Modal } from "../components/UI";
import Select from "../components/Select";
import { profileGroups } from "../profileGroups";
import {
  changedProfileFields,
  diffProfileLines,
  profileFields,
  profileValue,
} from "./profile-diff";
import "./profile-history.css";

function RevisionOrigin({ profile }) {
  const origin = profile.revision_origin;
  const agent = origin?.kind === "agent";
  const manual = origin?.kind === "manual";
  const Icon = agent ? Bot : manual ? UserRound : History;
  return (
    <span
      className="profile-revision-origin"
      data-origin={origin?.kind || "unknown"}
    >
      <Icon size={14} aria-hidden="true" />
      <span>
        {agent
          ? `Agent 修改${origin.agent_name ? ` · ${origin.agent_name}` : ""}`
          : manual
            ? "手动保存"
            : "历史版本 · 未记录来源"}
      </span>
    </span>
  );
}

function valueText(profile, key) {
  const value = profileValue(profile, key);
  if (key === "category") return profileGroups[value] || value;
  return typeof value === "string" ? value : JSON.stringify(value, null, 2);
}

function BodyDiff({ before, after }) {
  const diff = useMemo(() => diffProfileLines(before, after), [before, after]);
  const [context, setContext] = useState(false);
  const [limit, setLimit] = useState(200);
  const rows = context
    ? diff.rows
    : diff.rows.filter((row) => row.type !== "same");
  return (
    <section className="profile-body-diff" aria-label="正文更新差异">
      <div className="profile-diff-toolbar">
        <strong>正文</strong>
        <span className="profile-diff-added">+{diff.added} 行新增</span>
        <span className="profile-diff-removed">−{diff.removed} 行删除</span>
        <label className="checkbox-label">
          <input
            type="checkbox"
            checked={context}
            onChange={(e) => {
              setContext(e.target.checked);
              setLimit(200);
            }}
          />
          显示未改动行
        </label>
      </div>
      <p className="muted">
        左侧为旧行号，右侧为新行号；修改显示为删除旧行并新增内容。
      </p>
      {diff.coarse && (
        <p className="muted">
          变更范围较大，按整段替换展示；可切换“完整内容”查看原文。
        </p>
      )}
      {!diff.added && !diff.removed && (
        <p className="profile-history-empty">正文没有变化。</p>
      )}
      <div className="profile-diff-lines">
        {rows.slice(0, limit).map((row, index) => (
          <div className={`profile-diff-line is-${row.type}`} key={index}>
            <span className="profile-line-number">{row.oldLine ?? ""}</span>
            <span className="profile-line-number">{row.newLine ?? ""}</span>
            <span
              aria-label={
                row.type === "added"
                  ? "新增"
                  : row.type === "removed"
                    ? "删除"
                    : "未改动"
              }
            >
              {row.type === "added" ? "+" : row.type === "removed" ? "−" : " "}
            </span>
            <code>{row.text || <span className="muted">（空行）</span>}</code>
          </div>
        ))}
      </div>
      {rows.length > limit && (
        <Button onClick={() => setLimit((n) => n + 200)}>
          显示更多行（剩余 {rows.length - limit} 行）
        </Button>
      )}
    </section>
  );
}

export default function ProfileHistory({
  profile,
  hasUnsaved,
  onClose,
  onRestore,
  notify,
}) {
  const detail = useRef(null);
  const activeVersion = useRef(null);
  const [history, setHistory] = useState([]);
  const [selected, setSelected] = useState(null);
  const [baseline, setBaseline] = useState("");
  const [tab, setTab] = useState("changes");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const [restoring, setRestoring] = useState(false);
  useEffect(() => {
    let current = true;
    setLoading(true);
    setError("");
    api(`/profiles/${profile.id}/versions`)
      .then((versions) => {
        if (!current) return;
        const sorted = [...versions].sort((a, b) => b.version - a.version);
        setHistory(sorted);
        setSelected(sorted[0] || null);
        setBaseline(sorted[1] ? String(sorted[1].version) : "");
      })
      .catch((e) => current && setError(e.message))
      .finally(() => current && setLoading(false));
    return () => {
      current = false;
    };
  }, [profile.id, retry]);
  const previous =
    history.find((item) => String(item.version) === baseline) || null;
  const changes = useMemo(
    () => (selected ? changedProfileFields(previous, selected) : []),
    [previous, selected],
  );
  const selectVersion = (item, index) => {
    setSelected(item);
    setBaseline(history[index + 1] ? String(history[index + 1].version) : "");
  };
  useEffect(() => {
    detail.current?.scrollTo({ top: 0 });
  }, [selected?.version, baseline, tab]);
  useEffect(() => {
    const reveal = () =>
      activeVersion.current?.scrollIntoView({
        block: "nearest",
        inline: "nearest",
      });
    reveal();
    window.addEventListener("resize", reveal);
    return () => window.removeEventListener("resize", reveal);
  }, [selected?.version]);
  return (
    <Modal
      title="版本历史"
      wide
      className="profile-history-modal"
      onClose={() => !restoring && onClose()}
      footer={
        <>
          <span className="profile-history-footer-note">
            {hasUnsaved
              ? "当前有未保存修改，恢复会替换编辑区草稿并保存为新版本。"
              : "恢复会创建新版本，已有历史和已发布快照保持不变。"}
          </span>
          <Button disabled={restoring} onClick={onClose}>
            关闭
          </Button>
          <Button
            icon={RotateCcw}
            variant="primary"
            loading={restoring}
            disabled={
              loading || !selected || selected.version === history[0]?.version
            }
            onClick={async () => {
              setRestoring(true);
              try {
                await onRestore(selected);
              } finally {
                setRestoring(false);
              }
            }}
          >
            恢复此版本为新版本
          </Button>
        </>
      }
    >
      {loading ? (
        <p role="status">正在读取版本历史…</p>
      ) : error ? (
        <div role="alert">
          <p>{error}</p>
          <Button onClick={() => setRetry((n) => n + 1)}>重新加载</Button>
        </div>
      ) : !selected ? (
        <p>暂无保存的版本。</p>
      ) : (
        <div className="profile-history-layout">
          <nav
            className="history-list profile-history-timeline"
            aria-label="已保存版本"
          >
            <div className="profile-history-list-title">
              <History size={16} />
              更新记录 <Badge>{history.length}</Badge>
            </div>
            {history.map((item, index) => {
              const changed = changedProfileFields(history[index + 1], item);
              return (
                <button
                  key={item.version}
                  ref={
                    item.version === selected.version
                      ? activeVersion
                      : undefined
                  }
                  aria-current={
                    item.version === selected.version ? "true" : undefined
                  }
                  className={
                    item.version === selected.version ? "selected" : ""
                  }
                  onClick={() => selectVersion(item, index)}
                >
                  <strong>
                    v{item.version}
                    {index === 0 && <Badge>当前版本</Badge>}
                  </strong>
                  <time dateTime={item.updated_at}>
                    {formatTime(item.updated_at)}
                  </time>
                  <RevisionOrigin profile={item} />
                  <small>
                    {index === history.length - 1
                      ? "首次保存"
                      : changed.length
                        ? `修改了${changed.map(([, label]) => label).join("、")}`
                        : "内容未变，重新保存"}
                  </small>
                </button>
              );
            })}
          </nav>
          <section
            ref={detail}
            className="profile-history-detail"
            aria-label="版本详情"
          >
            <header className="profile-history-heading">
              <h3>{selected.name}</h3>
              <Badge>v{selected.version}</Badge>
              <CopyButton
                key={selected.version}
                value={selected.body}
                label="复制此版本正文"
                notify={notify}
              />
            </header>
            <p className="muted">
              保存于 {formatTime(selected.updated_at)} ·{" "}
              {valueText(selected, "category")}
            </p>
            <RevisionOrigin profile={selected} />
            <div className="tabs" role="tablist" aria-label="版本查看方式">
              <button
                role="tab"
                id="profile-history-changes-tab"
                aria-controls="profile-history-changes"
                aria-selected={tab === "changes"}
                className={tab === "changes" ? "active" : ""}
                onClick={() => setTab("changes")}
              >
                更新内容
              </button>
              <button
                role="tab"
                id="profile-history-content-tab"
                aria-controls="profile-history-content"
                aria-selected={tab === "content"}
                className={tab === "content" ? "active" : ""}
                onClick={() => setTab("content")}
              >
                完整内容
              </button>
            </div>
            {tab === "changes" ? (
              <div
                role="tabpanel"
                id="profile-history-changes"
                aria-labelledby="profile-history-changes-tab"
              >
                <div className="profile-history-comparison">
                  <Field label="对比基准">
                    <Select
                      value={baseline}
                      onChange={(e) => setBaseline(e.target.value)}
                    >
                      <option value="">空白版本（全部内容）</option>
                      {history
                        .filter((item) => item.version !== selected.version)
                        .map((item) => (
                          <option
                            key={item.version}
                            value={String(item.version)}
                          >
                            v{item.version} · {formatTime(item.updated_at)}
                          </option>
                        ))}
                    </Select>
                  </Field>
                  <p>
                    {previous
                      ? `v${previous.version} → v${selected.version}`
                      : `空白 → v${selected.version}`}
                  </p>
                </div>
                {!changes.length && (
                  <p className="profile-history-empty">与对比版本内容一致。</p>
                )}
                <BodyDiff
                  key={`${selected.version}:${baseline}`}
                  before={previous?.body || ""}
                  after={selected.body || ""}
                />
                {changes
                  .filter(([key]) => key !== "body")
                  .map(([key, label]) => (
                    <section
                      className="profile-metadata-change"
                      key={key}
                      aria-label={`${label}变更`}
                    >
                      <h4>{label}</h4>
                      <div>
                        <span>之前</span>
                        <pre className="is-removed">
                          {valueText(previous, key) || "（空）"}
                        </pre>
                      </div>
                      <div>
                        <span>之后</span>
                        <pre className="is-added">
                          {valueText(selected, key) || "（空）"}
                        </pre>
                      </div>
                    </section>
                  ))}
              </div>
            ) : (
              <div
                role="tabpanel"
                id="profile-history-content"
                aria-labelledby="profile-history-content-tab"
              >
                <p className="muted">此版本保存的原始内容，占位符保持原样。</p>
                <pre
                  className="profile-history-body"
                  aria-label="历史版本完整正文"
                >
                  {selected.body}
                </pre>
                {profileFields
                  .filter(
                    ([key]) =>
                      key !== "body" && key !== "name" && key !== "category",
                  )
                  .map(([key, label]) => (
                    <details className="profile-history-metadata" key={key}>
                      <summary>{label}</summary>
                      <pre>{valueText(selected, key) || "（空）"}</pre>
                    </details>
                  ))}
              </div>
            )}
          </section>
        </div>
      )}
    </Modal>
  );
}
