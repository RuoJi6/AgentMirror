import React, { useMemo, useState } from "react";

export const CONTENT_PAGE_SIZE = 8000;

// Pretty printing is opt-in for large results and bounded for deep/wide JSON.
// Remote content is always a React text child, never HTML or Markdown.
export function formatContent(text) {
  if (text.length > 32768) return text;
  try {
    const value = JSON.parse(text);
    const stack = [[value, 0]];
    let nodes = 0;
    while (stack.length) {
      const [item, depth] = stack.pop();
      if (++nodes > 2000 || depth > 12) return text;
      if (item && typeof item === "object")
        for (const child of Object.values(item)) stack.push([child, depth + 1]);
    }
    const formatted = JSON.stringify(value, null, 2);
    return formatted.length <= 65536 ? formatted : text;
  } catch {
    return text;
  }
}

export function valueText(value) {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    return "内容结构过深，请下载原始回传查看。";
  }
}

export default function TextContent({
  text,
  label = "内容",
  expanded = false,
}) {
  const [open, setOpen] = useState(expanded);
  const [page, setPage] = useState(0);
  const formatted = useMemo(() => formatContent(text), [text]);
  const pages = Math.max(1, Math.ceil(formatted.length / CONTENT_PAGE_SIZE));
  const currentPage = Math.min(page, pages - 1);
  const long = formatted.length > 1200;
  const visible =
    long && !open
      ? formatted.slice(0, 1200)
      : formatted.slice(
          currentPage * CONTENT_PAGE_SIZE,
          (currentPage + 1) * CONTENT_PAGE_SIZE,
        );
  return (
    <div className="text-content">
      <pre className="json-view" aria-label={label}>
        {visible}
        {long && !open ? "\n…" : ""}
      </pre>
      {long && (
        <div className="content-controls">
          <button className="text-button" onClick={() => setOpen(!open)}>
            {open ? "收起内容" : "查看完整内容"}
          </button>
          <span>{formatted.length.toLocaleString()} 字符</span>
          {open && pages > 1 && (
            <>
              <button
                className="button"
                disabled={currentPage === 0}
                onClick={() => setPage(currentPage - 1)}
              >
                上一段
              </button>
              <span aria-live="polite">
                第 {currentPage + 1} / {pages} 段
              </span>
              <button
                className="button"
                disabled={currentPage === pages - 1}
                onClick={() => setPage(currentPage + 1)}
              >
                下一段
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
