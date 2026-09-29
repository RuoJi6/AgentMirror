import { workspaceSiteNodes, siteNodeName } from "./workspace-site-paths";
import Select from "../components/Select";
import React, { useState } from "react";
import {
  Plus,
  Network,
  Pencil,
  Play,
  Pause,
  Trash2,
  Save,
  ExternalLink,
} from "lucide-react";
import { useApp } from "../App";
import { api } from "../api";
import {
  Panel,
  Button,
  Field,
  Modal,
  Confirm,
  Callout,
  Badge,
  CopyButton,
  Empty,
} from "../components/UI";

export default function Ports() {
  const { data, refresh, notify, navigate } = useApp();
  const [draft, setDraft] = useState(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [deleting, setDeleting] = useState(null);
  const listeners = data.listeners || [];
  const deployments = data.deployments.filter((d) => d.mode === "composable");
  const deployment = deployments.find((d) => d.id === draft?.deployment_id);
  const workspace = data.workspaces.find(
    (w) => w.id === deployment?.workspace_id,
  );
  const patch = (values) => setDraft((d) => ({ ...d, ...values }));
  const open = (item) => {
    setError("");
    const usable = deployments.some((d) => d.id === item.deployment_id);
    setDraft({
      ...structuredClone(item),
      deployment_id: usable ? item.deployment_id : "",
      enabled: usable ? item.enabled : false,
      root_surface: "page",
      overrides: null,
    });
  };
  const create = () => {
    const used = new Set([
      ...listeners.map((l) => Number(l.port)),
      data.runtime.admin_port,
      data.runtime.public_port,
    ]);
    let port =
      Math.max(
        data.runtime.public_port || 8765,
        data.runtime.admin_port || 8766,
        8766,
      ) + 1;
    if (port > 65535) port = 8767;
    while (used.has(port) && port <= 65535) port++;
    if (port > 65535) {
      notify("没有可用的默认端口，请先释放端口", "error");
      return;
    }
    const url = new URL(data.settings.public_url);
    url.protocol = "http:";
    url.port = String(port);
    const target =
      deployments.find((d) => d.id === data.settings.default_deployment) ||
      deployments[0];
    open({
      name: "",
      host: "0.0.0.0",
      port,
      public_url: url.origin,
      deployment_id: target?.id || "",
      enabled: !!target,
      root_surface: "page",
      overrides: null,
    });
  };
  const save = async (item, close = false) => {
    setBusy(true);
    setError("");
    try {
      const existingLegacy =
        item.id &&
        item.deployment_id &&
        !deployments.some((d) => d.id === item.deployment_id);
      await api(
        "/listeners",
        "POST",
        existingLegacy
          ? item
          : { ...item, root_surface: "page", overrides: null },
      );
      await refresh();
      notify(
        item.enabled ? "端口配置已生效" : "端口已暂停，配置和历史会话已保留",
      );
      if (close) setDraft(null);
    } catch (e) {
      setError(e.message);
      notify(e.message, "error");
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Panel
        title="多端口管理"
        className="ports-panel"
        action={
          <Button icon={Plus} variant="primary" onClick={create}>
            新增端口
          </Button>
        }
      >
        <div className="ports-intro">
          <Network size={20} />
          <div>
            <strong>发布工作区的测试入口</strong>
            <p>
              每个端口绑定已发布工作区中的主站点或子站点，从 /
              访问所选站点及其下级。
            </p>
          </div>
          <Badge tone="purple">
            {listeners.filter((l) => l.status === "running").length} 个运行中
          </Badge>
        </div>
        {!listeners.length ? (
          <Empty
            icon={Network}
            title="暂无端口配置"
            description="发布蜜罐工作区后，创建测试端口获得访问入口。"
          >
            <Button icon={Plus} onClick={create}>
              创建第一个端口
            </Button>
          </Empty>
        ) : (
          <div className="port-list">
            {listeners.map((item) => {
              const bound = deployments.find(
                (d) => d.id === item.deployment_id,
              );
              const legacy = (data.legacy_deployments || []).find(
                (d) => d.id === item.deployment_id,
              );
              const legacyBinding = !!item.deployment_id && !bound;
              return (
                <article
                  className="port-card"
                  data-role="public"
                  data-id={item.id}
                  key={item.id}
                >
                  <div className="port-card-main">
                    <div className="port-number">
                      <Network size={17} />
                      <strong>{item.port}</strong>
                    </div>
                    <div className="port-card-title">
                      <h3>
                        {item.name}{" "}
                        {item.is_default && (
                          <Badge tone="purple">默认入口</Badge>
                        )}
                      </h3>
                      <span>
                        {item.host}:{item.port} ·{" "}
                        {bound?.name || legacy?.name || "未绑定工作区"}
                      </span>
                    </div>
                    <Badge
                      tone={
                        item.status === "running"
                          ? "green"
                          : item.status === "error"
                            ? "red"
                            : ""
                      }
                    >
                      {item.status === "running"
                        ? "运行中"
                        : item.status === "error"
                          ? "启动失败"
                          : "已暂停"}
                    </Badge>
                  </div>
                  {bound ? (
                    <div className="port-binding">
                      <span>
                        工作区 <strong>{bound.name}</strong>
                      </span>
                      <span>
                        发布快照{" "}
                        <strong>第 {bound.revision || 1} 次发布</strong>
                      </span>
                      <span>
                        首页站点{" "}
                        <strong>
                          {siteNodeName(
                            data.workspaces.find(
                              (w) => w.id === bound.workspace_id,
                            ) || {},
                            item.site_node_id,
                          )}{" "}
                          · /
                        </strong>
                      </span>
                    </div>
                  ) : (
                    <p className="port-binding">
                      {legacyBinding
                        ? "旧版绑定可启停和删除，编排入口已移除。需要修改内容时，请改绑到已发布工作区。"
                        : "先发布蜜罐工作区，再将其绑定到此端口。"}
                    </p>
                  )}
                  {legacyBinding && <Badge>旧版绑定 · 只读</Badge>}
                  {item.error && (
                    <p className="port-error" role="alert">
                      {item.error}
                    </p>
                  )}
                  {bound && !bound.enabled && (
                    <p className="port-error">
                      工作区已暂停，测试路径暂不可访问。可在工作区恢复发布访问。
                    </p>
                  )}
                  <div className="port-card-footer">
                    <code>{item.public_url}/</code>
                    <div className="actions">
                      {bound && (
                        <CopyButton
                          value={`${item.public_url}/`}
                          notify={notify}
                        />
                      )}
                      {bound && item.status === "running" && (
                        <a
                          className="button"
                          href={`${item.public_url}/`}
                          target="_blank"
                          rel="noreferrer"
                        >
                          <ExternalLink size={15} />
                          打开
                        </a>
                      )}
                      <Button icon={Pencil} onClick={() => open(item)}>
                        {legacyBinding ? "改绑工作区" : "配置"}
                      </Button>
                      {bound && !item.is_default && item.enabled && (
                        <Button
                          disabled={busy}
                          onClick={async () => {
                            setBusy(true);
                            try {
                              await api(
                                `/listeners/${item.id}/default`,
                                "POST",
                                {},
                              );
                              await refresh();
                              notify("默认入口已切换");
                            } catch (e) {
                              notify(e.message, "error");
                            } finally {
                              setBusy(false);
                            }
                          }}
                        >
                          设为默认入口
                        </Button>
                      )}
                      {(bound || legacyBinding) && (
                        <Button
                          icon={item.status === "running" ? Pause : Play}
                          disabled={busy}
                          onClick={() =>
                            save({
                              ...item,
                              enabled: item.status !== "running",
                            })
                          }
                        >
                          {item.status === "running"
                            ? "暂停端口"
                            : item.status === "error"
                              ? "重试启动"
                              : "启用端口"}
                        </Button>
                      )}
                      <button
                        className="icon-button danger-text"
                        aria-label={`删除端口 ${item.port}`}
                        disabled={busy}
                        onClick={() => {
                          setError("");
                          setDeleting(item);
                        }}
                      >
                        <Trash2 size={17} />
                      </button>
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        )}
      </Panel>
      {draft && (
        <Modal
          wide
          title={draft.id ? `端口配置 · ${draft.port}` : "新增测试端口"}
          onClose={() => !busy && setDraft(null)}
          footer={
            <>
              <Button disabled={busy} onClick={() => setDraft(null)}>
                取消
              </Button>
              <Button
                icon={Save}
                variant="primary"
                loading={busy}
                onClick={() => save(draft, true)}
              >
                保存端口
              </Button>
            </>
          }
        >
          <div className="form-grid">
            <Field label="端口名称">
              <input
                value={draft.name}
                placeholder="例如：配置中心测试入口"
                onChange={(e) => patch({ name: e.target.value })}
              />
            </Field>
            <Field label="监听端口" hint="不能与管理后台或其他测试端口重复。">
              <input
                type="number"
                min="1"
                max="65535"
                value={draft.port}
                onChange={(e) => {
                  const port = Number(e.target.value);
                  let public_url = draft.public_url;
                  try {
                    const u = new URL(public_url);
                    if (port >= 1 && port <= 65535) {
                      u.port = String(port);
                      public_url = u.origin;
                    }
                  } catch {}
                  patch({ port, public_url });
                }}
              />
            </Field>
            <Field
              label="监听地址"
              hint="0.0.0.0 可供局域网访问；127.0.0.1 仅供本机。运行中修改地址需先暂停。"
            >
              <input
                value={draft.host}
                onChange={(e) => patch({ host: e.target.value })}
              />
            </Field>
            <Field
              label="此端口公告地址"
              hint="用于测试链接和回传，填写被测设备可访问的 IP / 域名及此端口。"
            >
              <input
                value={draft.public_url}
                onChange={(e) => patch({ public_url: e.target.value })}
              />
            </Field>
            <Field
              label="绑定工作区"
              hint="草稿需先在蜜罐工作区发布，才会出现在这里。"
            >
              <Select
                value={draft.deployment_id || ""}
                onChange={(e) =>
                  patch({
                    deployment_id: e.target.value,
                    site_node_id: "root",
                    enabled: e.target.value ? draft.enabled : false,
                  })
                }
              >
                <option value="">未绑定工作区</option>
                {deployments.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name}
                    {!d.enabled ? "（已暂停）" : ""}
                  </option>
                ))}
              </Select>
            </Field>
            {deployment && (
              <Field
                label="端口首页站点"
                hint="独立端口下使用站内路径，例如 /downloads/vpn.zip。"
              >
                <Select
                  value={draft.site_node_id || "root"}
                  onChange={(e) => patch({ site_node_id: e.target.value })}
                >
                  {workspaceSiteNodes(workspace || {}).map((n) => (
                    <option key={n.id} value={n.id}>
                      {n.name} · {n.mount_path}
                    </option>
                  ))}
                </Select>
              </Field>
            )}
          </div>
          {deployment ? (
            <Callout>
              保存站点或场景后，此端口自动更新；已有会话后续请求使用新版，历史交付记录保留。独立端口的会话和回传按端口隔离。
              <Button
                onClick={() =>
                  navigate("composer", deployment.workspace_id || deployment.id)
                }
              >
                打开工作区
              </Button>
            </Callout>
          ) : (
            <Callout>
              尚未绑定已发布工作区，保存后端口保持暂停。
              <Button onClick={() => navigate("composer")}>
                前往蜜罐工作区
              </Button>
            </Callout>
          )}
          <label className="port-option">
            <input
              type="checkbox"
              checked={draft.enabled}
              disabled={!deployment}
              onChange={(e) => patch({ enabled: e.target.checked })}
            />
            保存后启用此端口
          </label>
          {error && (
            <p role="alert" className="port-error">
              {error}
            </p>
          )}
        </Modal>
      )}
      {deleting && (
        <Confirm
          title="删除端口"
          description={`停止监听 ${deleting.host}:${deleting.port} 并删除端口配置。工作区、提示词和历史会话保留。`}
          loading={busy}
          onClose={() => !busy && setDeleting(null)}
          onConfirm={async () => {
            setBusy(true);
            try {
              await api(`/listeners/${deleting.id}`, "DELETE");
              await refresh();
              setDeleting(null);
              notify("端口已删除");
            } catch (e) {
              notify(e.message, "error");
            } finally {
              setBusy(false);
            }
          }}
        />
      )}
    </>
  );
}
