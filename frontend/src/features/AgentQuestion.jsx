import React, { useId, useRef, useState } from "react";
import { MessageCircleQuestion } from "lucide-react";
import { api } from "../api";
import AgentAttachments from "./AgentAttachments";
import { Button, Field } from "../components/UI";

export default function AgentQuestion({
  turn,
  canAnswer,
  disabled,
  onContinue,
  onCancel,
}) {
  const question = turn.question;
  const [answer, setAnswer] = useState("");
  const [attachments, setAttachments] = useState(turn.attachments || []);
  const [uploading, setUploading] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const submitting = useRef(false);
  const titleID = useId();
  if (!question) return null;
  const waiting = turn.status === "waiting_user" && canAnswer;
  const act = async (action) => {
    if (submitting.current || uploading) return;
    submitting.current = true;
    setBusy(action);
    setError("");
    try {
      const next = await api(
        `/generation/jobs/${turn.id}/${action}`,
        "POST",
        action === "answer"
          ? {
              question_id: question.id,
              answer: answer.trim(),
              ...(turn.mode !== "review"
                ? { attachments: attachments.map(({ id }) => ({ id })) }
                : {}),
            }
          : {},
      );
      await (action === "answer" ? onContinue(next) : onCancel(next));
    } catch (e) {
      setError(e.message);
    } finally {
      submitting.current = false;
      setBusy("");
    }
  };
  return (
    <section className="agent-question" aria-labelledby={titleID}>
      <strong id={titleID}>
        <MessageCircleQuestion size={18} />
        {question.text}
      </strong>
      {question.answer ? (
        <p className="agent-question-answer">你的回答：{question.answer}</p>
      ) : waiting ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (answer.trim() && !disabled && !uploading) act("answer");
          }}
        >
          {!!question.options?.length && (
            <div className="agent-question-options" aria-label="建议回答">
              {question.options.map((option) => (
                <Button
                  key={option}
                  type="button"
                  aria-pressed={answer === option}
                  disabled={!!busy || disabled}
                  onClick={() => setAnswer(option)}
                >
                  {option}
                </Button>
              ))}
            </div>
          )}
          <AgentAttachments
            enabled={turn.mode !== "review"}
            files={attachments}
            onChange={setAttachments}
            onBusyChange={setUploading}
            disabled={!!busy || disabled}
          >
            <Field
              label="回答 Agent"
              hint="可以选择建议，也可以输入自己的回答。进度已保存，提交后继续原任务。"
            >
              <textarea
                value={answer}
                rows={3}
                maxLength={12000}
                disabled={!!busy || disabled}
                onChange={(e) => setAnswer(e.target.value)}
                placeholder="输入你的回答…"
              />
            </Field>
          </AgentAttachments>
          <div className="agent-question-actions">
            <Button
              type="submit"
              variant="primary"
              loading={busy === "answer"}
              disabled={!answer.trim() || uploading || !!busy || disabled}
            >
              提交回答并继续
            </Button>
            <Button
              type="button"
              loading={busy === "cancel"}
              disabled={!!busy || uploading || disabled}
              onClick={() => act("cancel")}
            >
              取消等待
            </Button>
          </div>
        </form>
      ) : (
        <small className="muted">
          {turn.status === "cancelled"
            ? "已取消等待"
            : "请在所属工作区的当前对话中回答。"}
        </small>
      )}
      {error && (
        <p className="composer-error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}
