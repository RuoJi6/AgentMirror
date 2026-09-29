import React, { useEffect, useId, useMemo, useRef, useState } from "react";
import {
  ArrowLeft,
  AtSign,
  ChevronRight,
  FileText,
  Globe,
  Layers,
  X,
} from "lucide-react";

const emptyItems = [];
const kinds = {
  site: { label: "站点素材", aliases: ["站点", "素材", "site"], Icon: Globe },
  scenario: { label: "模拟场景", aliases: ["场景", "scenario"], Icon: Layers },
  profile: { label: "提示词", aliases: ["方案", "profile"], Icon: FileText },
};
const kindEntries = Object.entries(kinds);
const referenceKey = (item) => `${item.kind}:${item.id}`;
const referenceName = (item) => item.name || item.id;

export function ReferenceChips({ references = emptyItems, label, onRemove }) {
  if (!references.length) return null;
  return (
    <div className="agent-reference-chips" aria-label={label}>
      {references.map((item) => {
        const { label: kind, Icon } = kinds[item.kind] || kinds.site;
        const title = `${kind} · ${referenceName(item)} · v${item.version || 1}`;
        return (
          <span
            className="agent-reference-chip"
            key={referenceKey(item)}
            title={title}
          >
            <Icon size={13} aria-hidden="true" />
            <span>
              {kind} · {referenceName(item)}
            </span>
            <small>v{item.version || 1}</small>
            {onRemove && (
              <button
                type="button"
                aria-label={`移除引用 ${referenceName(item)}`}
                onClick={() => onRemove(item)}
              >
                <X size={13} aria-hidden="true" />
              </button>
            )}
          </span>
        );
      })}
    </div>
  );
}

// The typed @ query is only a picker trigger. Selected identities live in references.
function triggerAt(value, caret) {
  const before = value.slice(0, caret);
  // Chinese prose need not insert spaces before @; ASCII email addresses still
  // require a separator so foo@bar remains ordinary request text.
  const match =
    /(?:^|[\s(\p{Script=Han}\u3000-\u303f\uff01-\uff65“”‘’—…])@([^@\n\r]*)$/u.exec(
      before,
    );
  if (!match) return null;
  return { start: caret - match[1].length - 1, end: caret, query: match[1] };
}

function parseQuery(raw) {
  const value = raw.trimStart();
  for (const [kind, { label, aliases }] of kindEntries) {
    for (const prefix of [label, ...aliases]) {
      if (
        value.toLocaleLowerCase().startsWith(prefix.toLocaleLowerCase()) &&
        (value.length === prefix.length || /[\s:：]/.test(value[prefix.length]))
      )
        return {
          category: kind,
          query: value
            .slice(prefix.length)
            .replace(/^[\s:：]+/, "")
            .trim(),
        };
    }
  }
  return { category: null, query: value.trim() };
}

export default function MentionInput({
  value,
  onChange,
  inputRef,
  references,
  onReferencesChange,
  sites = emptyItems,
  scenarios = emptyItems,
  profiles = emptyItems,
  placeholder,
  onSubmit,
  disabled = false,
  hint = "发送时使用素材最新版；本轮可修改的模块由“修改范围”限定。",
}) {
  const [trigger, setTrigger] = useState(null);
  const [activeIndex, setActiveIndex] = useState(0);
  const [notice, setNotice] = useState("");
  const root = useRef(null);
  const menu = useRef(null);
  const optionList = useRef(null);
  const composing = useRef(false);
  const selectionFrame = useRef(null);
  const dismissed = useRef("");
  const listID = useId();
  const catalogue = useMemo(
    () => [
      ...sites.map((item) => ({
        kind: "site",
        id: item.id,
        version: item.version,
        name: item.name,
      })),
      ...scenarios.map((item) => ({
        kind: "scenario",
        id: item.id,
        version: item.version,
        name: item.name,
      })),
      ...profiles.map((item) => ({
        kind: "profile",
        id: item.id,
        version: item.version,
        name: item.name,
      })),
    ],
    [sites, scenarios, profiles],
  );
  const { category, query: search } = parseQuery(trigger?.query || "");
  const query = search.toLocaleLowerCase();
  const options = useMemo(() => {
    const categories = category
      ? []
      : kindEntries
          .filter(
            ([, { label, aliases }]) =>
              !query ||
              [label, ...aliases].some((term) =>
                term.toLocaleLowerCase().includes(query),
              ),
          )
          .map(([kind]) => ({
            kind,
            isCategory: true,
            count: catalogue.filter((item) => item.kind === kind).length,
          }));
    const results =
      category || query
        ? catalogue.filter(
            (item) =>
              (!category || item.kind === category) &&
              `${referenceName(item)} ${item.id}`
                .toLocaleLowerCase()
                .includes(query),
          )
        : [];
    return [...categories, ...results];
  }, [catalogue, category, query]);
  const isTypeMenu = !category && !query;
  const resultCount = options.filter((item) => !item.isCategory).length;
  const currentIndex = Math.min(activeIndex, Math.max(0, options.length - 1));
  const open = trigger !== null && !disabled;

  useEffect(() => {
    if (!value) {
      setTrigger(null);
      dismissed.current = "";
    }
  }, [value]);
  useEffect(() => () => cancelSelectionFrame(), []);
  useEffect(() => {
    if (!open) return;
    const outside = (event) => {
      if (!root.current?.contains(event.target)) close();
    };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [open, trigger]);
  useEffect(() => {
    if (!open) return;
    const container = menu.current;
    const bounds = container?.getBoundingClientRect();
    if (bounds && (bounds.top < 0 || bounds.bottom > window.innerHeight))
      container.scrollIntoView({ block: "nearest" });
  }, [open, category, options.length]);
  useEffect(() => {
    if (!open) return;
    const list = optionList.current;
    const option = document.getElementById(`${listID}-${currentIndex}`);
    if (!list || !option) return;
    const listBounds = list.getBoundingClientRect();
    const optionBounds = option.getBoundingClientRect();
    // Keep arrow-key selection visible inside the picker without scrolling the
    // page and leaving the picker's heading above the viewport.
    if (optionBounds.top < listBounds.top)
      list.scrollTop -= listBounds.top - optionBounds.top;
    else if (optionBounds.bottom > listBounds.bottom)
      list.scrollTop += optionBounds.bottom - listBounds.bottom;
  }, [currentIndex, open, listID, query, category]);

  function close() {
    dismissed.current = trigger
      ? `${trigger.start}:${trigger.end}:${trigger.query}`
      : "";
    setTrigger(null);
  }
  function cancelSelectionFrame() {
    if (selectionFrame.current !== null) {
      cancelAnimationFrame(selectionFrame.current);
      selectionFrame.current = null;
    }
  }
  function restoreCaret(caret) {
    cancelSelectionFrame();
    selectionFrame.current = requestAnimationFrame(() => {
      selectionFrame.current = null;
      inputRef.current?.focus({ preventScroll: true });
      inputRef.current?.setSelectionRange(caret, caret);
    });
  }
  function syncTrigger(next, caret) {
    const found = triggerAt(next, caret);
    const signature = found ? `${found.start}:${found.end}:${found.query}` : "";
    if (signature && signature === dismissed.current) return;
    if (!found) dismissed.current = "";
    if (
      !trigger ||
      found?.query !== trigger.query ||
      found?.start !== trigger.start
    )
      setActiveIndex(0);
    setTrigger(found);
  }
  function select(item) {
    if (!trigger) return;
    if (item.isCategory) {
      replaceQuery(`${kinds[item.kind].label} `);
      return;
    }
    const exists = references.some(
      (entry) => referenceKey(entry) === referenceKey(item),
    );
    const previousProfile = references.find(
      (entry) => entry.kind === "profile",
    );
    const next =
      item.kind === "profile"
        ? references.filter((entry) => entry.kind !== "profile")
        : references;
    if (!exists && next.length >= 8) {
      setNotice("最多引用 8 个素材，请先移除一个引用。");
      return;
    }
    if (!exists) onReferencesChange([...next, item]);
    else
      onReferencesChange(
        references.map((entry) =>
          referenceKey(entry) === referenceKey(item) &&
          item.version >= entry.version
            ? { ...entry, ...item }
            : entry,
        ),
      );
    setNotice(
      exists
        ? `已引用「${referenceName(item)}」，无需重复添加。`
        : item.kind === "profile" && previousProfile
          ? `已将提示词「${referenceName(previousProfile)}」替换为「${referenceName(item)}」。`
          : `已引用${kinds[item.kind].label}「${referenceName(item)}」。`,
    );
    const caret = trigger.start;
    onChange(value.slice(0, trigger.start) + value.slice(trigger.end));
    setTrigger(null);
    dismissed.current = "";
    restoreCaret(caret);
  }
  function replaceQuery(query) {
    if (!trigger) return;
    const next =
      value.slice(0, trigger.start) + `@${query}` + value.slice(trigger.end);
    const caret = trigger.start + query.length + 1;
    onChange(next);
    dismissed.current = "";
    setActiveIndex(0);
    setTrigger({ start: trigger.start, end: caret, query });
    restoreCaret(caret);
  }
  function insertTrigger() {
    const node = inputRef.current;
    const start = node?.selectionStart ?? value.length;
    const end = node?.selectionEnd ?? start;
    const prefix = start > 0 && !/[\s(（]/.test(value[start - 1]) ? " @" : "@";
    const next = value.slice(0, start) + prefix + value.slice(end);
    const caret = start + prefix.length;
    onChange(next);
    dismissed.current = "";
    setActiveIndex(0);
    setTrigger({ start: caret - 1, end: caret, query: "" });
    restoreCaret(caret);
  }

  return (
    <div className="agent-mention-input" ref={root}>
      {open && (
        <div className="agent-mention-menu" ref={menu}>
          <div className="agent-mention-heading">
            <div>
              {!isTypeMenu && (
                <button
                  type="button"
                  className="agent-mention-back"
                  aria-label="返回引用类型"
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => replaceQuery("")}
                >
                  <ArrowLeft size={16} aria-hidden="true" />
                </button>
              )}
              <strong>
                {category
                  ? `搜索${kinds[category].label}`
                  : isTypeMenu
                    ? "选择引用类型"
                    : "搜索全部素材"}
              </strong>
            </div>
            <span>↑↓ 选择 · Enter 确认 · Esc 关闭</span>
          </div>
          <div
            id={listID}
            ref={optionList}
            className="agent-mention-options"
            role="listbox"
            aria-label={isTypeMenu ? "引用类型" : "可引用的素材"}
          >
            {options.map((item, index) => {
              const { label, Icon } = kinds[item.kind];
              const alreadySelected = references.some(
                (entry) => referenceKey(entry) === referenceKey(item),
              );
              return (
                <button
                  key={item.isCategory ? item.kind : referenceKey(item)}
                  id={`${listID}-${index}`}
                  type="button"
                  role="option"
                  aria-selected={index === currentIndex}
                  aria-label={
                    item.isCategory
                      ? `选择${label}`
                      : `${label} · ${referenceName(item)} · v${item.version || 1}`
                  }
                  className={`${index === currentIndex ? "is-active" : ""} ${item.isCategory ? "agent-mention-type" : "agent-mention-result"}`}
                  onMouseDown={(event) => event.preventDefault()}
                  onMouseMove={() => setActiveIndex(index)}
                  onClick={() => select(item)}
                >
                  {item.isCategory ? (
                    <>
                      <AtSign size={21} aria-hidden="true" />
                      <strong>{label}</strong>
                      <small className="agent-mention-count">
                        {item.count}
                      </small>
                      <ChevronRight size={18} aria-hidden="true" />
                    </>
                  ) : (
                    <>
                      <span className="agent-mention-record">
                        <span className="agent-mention-record-title">
                          <span className="agent-mention-kind">
                            <Icon size={12} aria-hidden="true" />
                            {label}
                          </span>
                          <strong>{referenceName(item)}</strong>
                        </span>
                        <small>
                          {label} · v{item.version || 1}
                        </small>
                      </span>
                      <span className="agent-mention-record-meta">
                        <small className="agent-mention-id" title={item.id}>
                          #{item.id}
                        </small>
                        {alreadySelected && (
                          <small className="agent-mention-selected">
                            已引用
                          </small>
                        )}
                      </span>
                    </>
                  )}
                </button>
              );
            })}
            {!options.length && (
              <p className="agent-mention-empty">
                {query
                  ? "没有找到匹配素材，试试其他名称或 ID。"
                  : `暂无已保存的${kinds[category]?.label || "素材"}，保存后即可在这里引用。`}
              </p>
            )}
          </div>
          <div className="agent-mention-menu-footer">
            {isTypeMenu
              ? "选择类型，或直接输入名称 / ID 检索"
              : `已显示全部 ${resultCount} 条${category ? "" : "匹配素材"}`}
          </div>
        </div>
      )}
      <ReferenceChips
        references={references}
        label="本轮引用"
        onRemove={(item) => {
          onReferencesChange(
            references.filter(
              (entry) => referenceKey(entry) !== referenceKey(item),
            ),
          );
          setNotice(`已移除「${referenceName(item)}」。`);
        }}
      />
      <textarea
        ref={inputRef}
        aria-label="自然语言生成需求"
        rows={3}
        value={value}
        disabled={disabled}
        placeholder={placeholder}
        aria-expanded={open}
        aria-controls={open ? listID : undefined}
        aria-autocomplete="list"
        aria-activedescendant={
          open && options.length ? `${listID}-${currentIndex}` : undefined
        }
        onCompositionStart={() => {
          cancelSelectionFrame();
          composing.current = true;
        }}
        onCompositionEnd={(event) => {
          composing.current = false;
          syncTrigger(
            event.currentTarget.value,
            event.currentTarget.selectionStart,
          );
        }}
        onChange={(event) => {
          cancelSelectionFrame();
          onChange(event.target.value);
          setNotice("");
          if (!composing.current)
            syncTrigger(event.target.value, event.target.selectionStart);
        }}
        onSelect={(event) => {
          if (
            !composing.current &&
            event.currentTarget.selectionStart ===
              event.currentTarget.selectionEnd
          )
            syncTrigger(
              event.currentTarget.value,
              event.currentTarget.selectionStart,
            );
        }}
        onKeyDown={(event) => {
          cancelSelectionFrame();
          if (
            composing.current ||
            event.nativeEvent.isComposing ||
            event.keyCode === 229
          )
            return;
          if (open) {
            if (
              category &&
              !query &&
              ["ArrowLeft", "Backspace"].includes(event.key) &&
              !event.ctrlKey &&
              !event.metaKey &&
              !event.altKey &&
              !event.shiftKey &&
              event.currentTarget.selectionStart ===
                event.currentTarget.selectionEnd
            ) {
              event.preventDefault();
              replaceQuery("");
              return;
            }
            if (["ArrowDown", "ArrowUp"].includes(event.key)) {
              event.preventDefault();
              setActiveIndex(
                options.length
                  ? (currentIndex +
                      (event.key === "ArrowDown" ? 1 : options.length - 1)) %
                      options.length
                  : 0,
              );
              return;
            }
            if (event.key === "Escape") {
              event.preventDefault();
              close();
              return;
            }
            if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              if (options[currentIndex]) select(options[currentIndex]);
              return;
            }
            if (event.key === "Tab") close();
          }
          if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
            event.preventDefault();
            onSubmit();
          }
        }}
      />
      <div className="agent-mention-footer">
        <button
          type="button"
          className="agent-mention-trigger"
          aria-label="引用素材"
          disabled={disabled}
          onClick={insertTrigger}
        >
          <AtSign size={15} />
          引用素材
        </button>
        <span>{hint}</span>
      </div>
      <span className="agent-mention-notice" role="status" aria-live="polite">
        {notice}
      </span>
    </div>
  );
}
