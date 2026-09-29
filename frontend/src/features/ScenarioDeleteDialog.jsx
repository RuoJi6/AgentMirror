import React, { useState } from "react";
import { Button, Modal } from "../components/UI";

export default function ScenarioDeleteDialog({
  scenario,
  workspaces,
  selected,
  onClose,
  onDelete,
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const references = workspaces.filter((workspace) =>
    workspace.bindings?.some((binding) => binding.scenario_id === scenario.id),
  );
  const remove = async () => {
    setBusy(true);
    setError("");
    try {
      await onDelete(scenario);
      onClose();
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      title="删除模拟场景"
      onClose={() => !busy && onClose()}
      footer={
        <>
          <Button disabled={busy} onClick={onClose}>
            取消
          </Button>
          <Button
            variant="danger"
            disabled={busy || references.length > 0}
            loading={busy}
            onClick={remove}
          >
            确认删除
          </Button>
        </>
      }
    >
      <p>
        删除「{scenario.name}
        」及其当前规则？它将从素材库中移除，已发布快照和历史记录会保留。
      </p>
      {references.length > 0 ? (
        <div role="alert" className="composer-error">
          <p>此场景仍被以下工作区引用，暂时不能删除：</p>
          <ul>
            {references.map((workspace) => (
              <li key={workspace.id}>{workspace.name}</li>
            ))}
          </ul>
          <p>
            请先在这些工作区的“绑定提示词”中移除对应场景实例，保存工作区后再删除。
          </p>
        </div>
      ) : selected ? (
        <p className="muted">当前未保存草稿中的对应场景实例也会移除。</p>
      ) : null}
      {error && (
        <p role="alert" className="composer-error">
          {error}
        </p>
      )}
    </Modal>
  );
}
