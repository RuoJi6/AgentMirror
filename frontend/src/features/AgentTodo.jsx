import React, { useEffect, useId, useRef, useState } from "react";
import { Check, ChevronDown, ListTodo, Play, Square } from "lucide-react";

// Conversation-level latest plan, like ARTEX's TodoPopover. Native popovers
// keep the small read-only panel above the composer's clipping/scroll regions.
export default function AgentTodo({ plan = [], disabled = false }) {
  const id = useId();
  const trigger = useRef(null);
  const panel = useRef(null);
  const [open, setOpen] = useState(false);
  const unavailable = disabled || !plan.length;
  const position = () => {
    if (!trigger.current || !panel.current) return;
    const rect = trigger.current.getBoundingClientRect();
    const width = Math.min(320, window.innerWidth - 24);
    const above = rect.top >= 160;
    Object.assign(panel.current.style, {
      width: `${width}px`,
      left: `${Math.max(12, Math.min(rect.right - width, window.innerWidth - width - 12))}px`,
      top: above ? "auto" : `${rect.bottom + 8}px`,
      bottom: above ? `${window.innerHeight - rect.top + 8}px` : "auto",
      maxHeight: `${Math.max(80, Math.min(320, above ? rect.top - 20 : window.innerHeight - rect.bottom - 20))}px`,
    });
  };
  useEffect(() => {
    if (!open) return;
    window.addEventListener("resize", position);
    window.addEventListener("scroll", position, true);
    return () => {
      window.removeEventListener("resize", position);
      window.removeEventListener("scroll", position, true);
    };
  }, [open]);
  useEffect(() => {
    if (unavailable && panel.current?.matches(":popover-open"))
      panel.current.hidePopover();
  }, [unavailable]);
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className="agent-todo-trigger"
        disabled={unavailable}
        title={unavailable ? "本会话暂无 Todo" : "查看最近 Todo"}
        popoverTarget={id}
        aria-expanded={open}
        aria-controls={id}
        aria-haspopup="dialog"
      >
        <ChevronDown
          size={12}
          className="agent-todo-chevron"
          aria-hidden="true"
        />
        <ListTodo size={13} aria-hidden="true" />
        Todo
      </button>
      <div
        ref={panel}
        id={id}
        popover="auto"
        role="dialog"
        aria-label="最近 Todo"
        className="agent-todo-popover"
        onBeforeToggle={(event) => {
          if (event.newState === "open") position();
        }}
        onToggle={(event) => setOpen(event.newState === "open")}
      >
        <p className="agent-todo-title">最近 Todo</p>
        <ul aria-label="计划步骤">
          {plan.map((item, index) => {
            const complete = ["completed", "done"].includes(item.status);
            const working = ["in_progress", "running"].includes(item.status);
            const Icon = complete ? Check : working ? Play : Square;
            const label = complete ? "已完成" : working ? "进行中" : "待处理";
            return (
              <li
                key={index}
                data-status={
                  complete ? "completed" : working ? "in_progress" : "pending"
                }
              >
                <Icon size={12} role="img" aria-label={label} />
                <span>{item.step}</span>
              </li>
            );
          })}
        </ul>
      </div>
    </>
  );
}
