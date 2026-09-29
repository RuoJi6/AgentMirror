import React, { useEffect, useRef, useState } from "react";
import { File, Paperclip, Upload, X } from "lucide-react";
import { uploadHostedFile } from "../api";
import { Button } from "../components/UI";
import "./agent-attachments.css";

const bytes = (size = 0) =>
  size < 1048576
    ? `${Math.ceil(size / 1024)} KiB`
    : `${(size / 1048576).toFixed(1)} MiB`;

export function AttachmentChips({ files = [], onRemove, disabled }) {
  if (!files.length) return null;
  return (
    <div className="agent-attachments" aria-label="上传附件">
      {files.map((file) => (
        <span className="agent-attachment" key={file.id} title={file.name}>
          <File size={15} aria-hidden="true" />
          <span>{file.name}</span>
          <small>{bytes(file.size)}</small>
          {onRemove && (
            <button
              type="button"
              aria-label={`移除附件 ${file.name}`}
              disabled={disabled}
              onClick={() => onRemove(file.id)}
            >
              <X size={14} />
            </button>
          )}
        </span>
      ))}
    </div>
  );
}

export default function AgentAttachments({
  files,
  onChange,
  onBusyChange,
  disabled,
  enabled = true,
  children,
}) {
  const input = useRef(null),
    controller = useRef(null),
    live = useRef(true),
    dragDepth = useRef(0);
  const [progress, setProgress] = useState(null),
    [error, setError] = useState("");
  const [dragging, setDragging] = useState(false);
  const resetDrag = () => {
    dragDepth.current = 0;
    setDragging(false);
  };
  useEffect(() => {
    live.current = true;
    // A cancelled drag or a drop outside this input must clear its highlight.
    const clear = () => {
      dragDepth.current = 0;
      setDragging(false);
    };
    window.addEventListener("dragend", clear);
    window.addEventListener("drop", clear);
    window.addEventListener("blur", clear);
    return () => {
      live.current = false;
      controller.current?.abort();
      window.removeEventListener("dragend", clear);
      window.removeEventListener("drop", clear);
      window.removeEventListener("blur", clear);
    };
  }, []);
  const upload = async (selected) => {
    if (!enabled || disabled || !selected.length || controller.current) return;
    if (files.length + selected.length > 8) {
      setError("每轮最多附加 8 个文件");
      return;
    }
    if (selected.some((file) => file.size > 128 * 1024 * 1024)) {
      setError("单个文件不得超过 128 MiB");
      return;
    }
    const abort = new AbortController();
    controller.current = abort;
    setError("");
    onBusyChange?.(true);
    try {
      for (const file of selected) {
        setProgress({ name: file.name, percent: 0 });
        const meta = await uploadHostedFile(
          file,
          (percent) => {
            if (live.current) setProgress({ name: file.name, percent });
          },
          abort.signal,
        );
        if (!live.current) return;
        onChange((previous) => [...previous, meta]);
      }
    } catch (e) {
      if (live.current)
        setError(
          e.name === "AbortError"
            ? "已取消上传，已完成的附件仍保留。"
            : e.message,
        );
    } finally {
      controller.current = null;
      if (live.current) {
        setProgress(null);
        onBusyChange?.(false);
      }
    }
  };
  const blockedReason = disabled
    ? "当前暂不能上传文件"
    : progress
      ? "文件上传中，请稍后再拖入"
      : files.length >= 8
        ? "每轮最多附加 8 个文件"
        : "";
  const hasFiles = (event) =>
    Array.from(event.dataTransfer?.types || []).includes("Files");
  if (!enabled) return children;
  return (
    <div
      className={`agent-file-dropzone${dragging ? " is-dragging" : ""}`}
      onDragEnter={(event) => {
        if (!hasFiles(event)) return;
        event.preventDefault();
        dragDepth.current += 1;
        setDragging(true);
      }}
      onDragOver={(event) => {
        if (!hasFiles(event)) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = blockedReason ? "none" : "copy";
      }}
      onDragLeave={() => {
        dragDepth.current = Math.max(0, dragDepth.current - 1);
        if (!dragDepth.current) setDragging(false);
      }}
      onDrop={(event) => {
        if (!hasFiles(event)) return;
        event.preventDefault();
        event.stopPropagation();
        resetDrag();
        if (blockedReason) {
          setError(blockedReason);
          return;
        }
        const items = Array.from(event.dataTransfer.items || []);
        if (items.some((item) => item.webkitGetAsEntry?.()?.isDirectory)) {
          setError("暂不支持拖入文件夹，请选择文件或先压缩为 ZIP。");
          return;
        }
        const selected = Array.from(event.dataTransfer.files || []);
        if (!selected.length) {
          setError("未识别到文件，请使用“上传文件”选择。");
          return;
        }
        upload(selected);
      }}
    >
      {children}
      {dragging && (
        <div className="agent-file-drop-hint" role="status">
          <Upload size={24} aria-hidden="true" />
          <strong>{blockedReason || "松开以上传文件"}</strong>
          {!blockedReason && <small>最多 8 个文件，每个 128 MiB</small>}
        </div>
      )}
      <div className="agent-upload">
        <AttachmentChips
          files={files}
          disabled={disabled || !!progress}
          onRemove={(id) =>
            onChange((previous) => previous.filter((file) => file.id !== id))
          }
        />
        <input
          ref={input}
          type="file"
          multiple
          hidden
          aria-label="上传对话附件"
          disabled={disabled || !!progress}
          onChange={(e) => {
            const selected = Array.from(e.target.files);
            e.target.value = "";
            upload(selected);
          }}
        />
        <div className="agent-upload-actions">
          <Button
            icon={Paperclip}
            type="button"
            variant="ghost"
            disabled={disabled || !!progress || files.length >= 8}
            onClick={() => input.current?.click()}
          >
            上传文件
          </Button>
          {progress ? (
            <>
              <span role="status">
                正在上传 {progress.name} · {progress.percent}%
              </span>
              <Button type="button" onClick={() => controller.current?.abort()}>
                取消上传
              </Button>
            </>
          ) : (
            <small className="muted">
              可拖入文件，最多 8 个，每个 128 MiB。发送后由 Agent 按要求托管。
            </small>
          )}
        </div>
        {error && (
          <p className="composer-error" role="alert">
            {error}
          </p>
        )}
      </div>
    </div>
  );
}
