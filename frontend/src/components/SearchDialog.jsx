import React, { useEffect, useRef, useState } from "react";
import {
  ArrowUpRight,
  Search,
  Layers,
  MessageSquare,
  Network,
} from "lucide-react";
import { Modal } from "./UI";

export default function SearchDialog({ data, navigation, navigate, onClose }) {
  const [query, setQuery] = useState("");
  const input = useRef(null);
  const list = useRef(null);
  useEffect(() => {
    input.current?.focus();
  }, []);
  const entries = [
    ...navigation.map(([page, name, icon]) => ({
      page,
      name,
      icon,
      group: "页面",
      detail: "工作空间",
    })),
    ...(data.workspaces || []).map((w) => ({
      page: "composer",
      id: w.id,
      name: w.name,
      detail: w.deployment_id ? `已发布 · ${w.slug}` : `草稿 · ${w.slug}`,
      group: "蜜罐工作区",
      icon: Layers,
    })),
    ...data.profiles.map((p) => ({
      page: "profiles",
      id: p.id,
      name: p.name,
      detail: `v${p.version}`,
      group: "提示词",
      icon: MessageSquare,
    })),
    ...(data.listeners || []).map((l) => ({
      page: "settings",
      name: l.name,
      detail: `${l.host}:${l.port}`,
      group: "端口",
      icon: Network,
    })),
  ];
  const needle = query.trim().toLowerCase();
  const results = needle
    ? entries.filter((e) =>
        `${e.name} ${e.detail} ${e.id || ""} ${e.group}`
          .toLowerCase()
          .includes(needle),
      )
    : entries.slice(0, navigation.length);
  const select = (entry) => {
    onClose();
    navigate(entry.page, entry.id);
  };
  return (
    <Modal title="搜索工作空间" onClose={onClose} className="search-dialog">
      <div className="command-input">
        <Search size={20} />
        <input
          ref={input}
          aria-label="搜索工作空间内容"
          placeholder="输入工作区、提示词或端口…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") {
              e.preventDefault();
              list.current?.querySelector("button")?.focus();
            }
            if (e.key === "Enter" && results[0]) {
              e.preventDefault();
              select(results[0]);
            }
          }}
        />
      </div>
      <div
        className="search-results"
        ref={list}
        onKeyDown={(e) => {
          if (!["ArrowDown", "ArrowUp"].includes(e.key)) return;
          e.preventDefault();
          const buttons = [...list.current.querySelectorAll("button")];
          const next =
            buttons.indexOf(document.activeElement) +
            (e.key === "ArrowDown" ? 1 : -1);
          if (next < 0) input.current.focus();
          else buttons[Math.min(next, buttons.length - 1)]?.focus();
        }}
      >
        <p className="search-caption">
          {needle ? `${results.length} 个结果` : "快速导航"}
        </p>
        {results.map((e, index) => (
          <button key={`${e.page}-${e.id || index}`} onClick={() => select(e)}>
            <e.icon size={18} />
            <span>
              <strong>{e.name}</strong>
              <small>
                {e.group} · {e.detail}
              </small>
            </span>
            <ArrowUpRight size={15} />
          </button>
        ))}
        {!results.length && (
          <div className="search-no-results">
            未找到匹配项，试试其他名称或端口号。
          </div>
        )}
      </div>
      <div className="search-help">
        <span>
          <kbd>↑</kbd> <kbd>↓</kbd> 选择 <kbd>Enter</kbd> 打开
        </span>
        <span>
          <kbd>Esc</kbd> 关闭
        </span>
      </div>
    </Modal>
  );
}
