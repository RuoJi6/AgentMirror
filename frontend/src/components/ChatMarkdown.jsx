import React, { memo, useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Check, Copy } from "lucide-react";
import "./chat-markdown.css";

function CodeBlock({ children }) {
  const code = React.Children.toArray(children).find(React.isValidElement);
  const content = String(code?.props.children ?? "");
  const language =
    code?.props.className?.match(/(?:^|\s)language-([^\s]+)/)?.[1] || "text";
  const [status, setStatus] = useState("");
  const timer = useRef();
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = async () => {
    clearTimeout(timer.current);
    try {
      await navigator.clipboard.writeText(content);
      setStatus("已复制");
    } catch {
      setStatus("复制失败，请选中文本复制");
    }
    timer.current = setTimeout(() => setStatus(""), 2500);
  };
  return (
    <div className="chat-code-block">
      <div className="chat-code-toolbar">
        <span>{language}</span>
        <span role="status">{status}</span>
        <button type="button" onClick={copy} aria-label="复制代码">
          {status === "已复制" ? <Check size={13} /> : <Copy size={13} />}
          复制
        </button>
      </div>
      <pre tabIndex={0}>{children}</pre>
    </div>
  );
}

const components = {
  pre: CodeBlock,
  a: ({ href, title, children }) =>
    href ? (
      <a href={href} title={title} target="_blank" rel="noopener noreferrer">
        {children}
      </a>
    ) : (
      <span>{children}</span>
    ),
  // A generated image URL is a reference, not an automatic network request.
  img: ({ src, alt }) =>
    src ? (
      <a href={src} target="_blank" rel="noopener noreferrer">
        图片：{alt || "查看图片"}
      </a>
    ) : (
      <span>{alt || "图片"}</span>
    ),
  table: ({ children }) => (
    <div
      className="chat-markdown-table"
      tabIndex={0}
      role="region"
      aria-label="表格"
    >
      <table>{children}</table>
    </div>
  ),
};
const plugins = [remarkGfm];

// No raw-HTML plugin or custom URL transform: retain react-markdown's escaping
// and protocol filtering. Only assistant prose uses this view, never tool data.
export default memo(function ChatMarkdown({ text }) {
  let markdown = text;
  // Preserve escapes in unfenced JSON examples instead of interpreting them
  // as Markdown punctuation. This is display-only, never a draft operation.
  if (/^\s*[\[{]/.test(text)) {
    try {
      JSON.parse(text);
      markdown = "```json\n" + text + "\n```";
    } catch {}
  }
  return (
    <div className="chat-markdown">
      <ReactMarkdown remarkPlugins={plugins} components={components}>
        {markdown}
      </ReactMarkdown>
    </div>
  );
});
