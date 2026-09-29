import React, { useEffect, useMemo, useRef, useId } from "react";
import TextContent, { valueText } from "./TextContent";
import Select from "./Select";
import {
  X,
  ChevronRight,
  Copy,
  Check,
  LoaderCircle,
  CircleAlert,
  Radar,
} from "lucide-react";
export function Button({
  children,
  icon: Icon,
  variant = "",
  className = "",
  loading = false,
  ...props
}) {
  return (
    <button
      className={`button ${variant} ${className}`}
      {...props}
      disabled={props.disabled || loading}
    >
      {loading ? (
        <LoaderCircle className="spin" size={16} />
      ) : (
        Icon && <Icon size={16} />
      )}
      <span>{children}</span>
    </button>
  );
}
export function Badge({ children, tone = "" }) {
  return <span className={`badge ${tone}`}>{children}</span>;
}
export function Status({ enabled }) {
  return (
    <span className={`status ${enabled ? "green" : "muted"}`}>
      <i />
      {enabled ? "运行中" : "已暂停"}
    </span>
  );
}
export function PageHeader({ title, description, children }) {
  return (
    <div className="page-header">
      <div>
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      <div className="actions">{children}</div>
    </div>
  );
}
export function Panel({ title, action, children, className = "", ...props }) {
  return (
    <section className={`panel ${className}`} {...props}>
      {title && (
        <div className="panel-header">
          <h2>{title}</h2>
          {action}
        </div>
      )}
      {children}
    </section>
  );
}
export function Field({ label, hint, children, className = "" }) {
  const id = useId();
  const control =
    React.isValidElement(children) &&
    ["input", "textarea", Select].includes(children.type)
      ? React.cloneElement(children, {
          "aria-labelledby": id,
          "aria-describedby": hint ? id + "-hint" : undefined,
        })
      : children;
  return (
    <label className={`field ${className}`}>
      <span id={id}>{label}</span>
      {control}
      {hint && <small id={id + "-hint"}>{hint}</small>}
    </label>
  );
}
export function Empty({
  title = "暂无记录",
  description,
  icon: Icon = Radar,
  children,
}) {
  return (
    <div className="empty">
      <Icon size={42} strokeWidth={1.4} />
      <h3>{title}</h3>
      <p>{description}</p>
      {children}
    </div>
  );
}
export function Callout({ children }) {
  return (
    <div className="callout">
      <CircleAlert size={17} />
      <div>{children}</div>
    </div>
  );
}
export function JsonView({ value }) {
  const text = useMemo(() => valueText(value), [value]);
  return <TextContent text={text} />;
}
export function Modal({
  title,
  onClose,
  children,
  wide = false,
  footer,
  className = "",
}) {
  const ref = useRef(null);
  const titleId = useId();
  useEffect(() => {
    const node = ref.current;
    node.showModal();
    return () => node.close();
  }, []);
  return (
    <dialog
      ref={ref}
      className={`modal ${wide ? "wide" : ""} ${className}`}
      aria-labelledby={titleId}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
    >
      <div className="modal-head">
        <h2 id={titleId}>{title}</h2>
        <button className="icon-button" aria-label="关闭" onClick={onClose}>
          <X size={20} />
        </button>
      </div>
      <div className="modal-body">{children}</div>
      {footer && <div className="modal-footer">{footer}</div>}
    </dialog>
  );
}
export function Confirm({
  title,
  description,
  onClose,
  onConfirm,
  loading,
  confirmLabel,
}) {
  return (
    <Modal
      title={title}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="danger" onClick={onConfirm} loading={loading}>
            {confirmLabel || `确认${title.includes("清除") ? "清除" : "删除"}`}
          </Button>
        </>
      }
    >
      <p className="confirm-copy">{description}</p>
    </Modal>
  );
}
export function LinkButton({ children, onClick }) {
  return (
    <button className="text-button" onClick={onClick}>
      {children}
      <ChevronRight size={16} />
    </button>
  );
}
export function CopyButton({ value, label = "复制", notify, ...props }) {
  const [copied, setCopied] = React.useState(false);
  return (
    <Button
      {...props}
      icon={copied ? Check : Copy}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1600);
        } catch {
          notify?.("复制失败，请手动选取文本", "error");
        }
      }}
    >
      {copied ? "已复制" : label}
    </Button>
  );
}
