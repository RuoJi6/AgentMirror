import React, { useState } from "react";
import { Plus, Trash2, FileCode2, Copy, Upload } from "lucide-react";
import { api, uploadHostedFile } from "../api";
import { Button, Field, CopyButton, Panel } from "../components/UI";
import Select from "../components/Select";
import "./workspace-sites.css";

import { workspaceSiteNodes, siteEntryLink } from "./workspace-site-paths";
export { workspaceSiteNodes } from "./workspace-site-paths";

export function WorkspaceSites({
  workspace,
  sites,
  patch,
  onEdit,
  notify,
  listeners = [],
  onBind,
  canBind = false,
}) {
  const nodes = workspaceSiteNodes(workspace);
  const mounts = workspace.site_mounts || [];
  const update = (id, values) =>
    patch({
      site_mounts: mounts.map((m) => (m.id === id ? { ...m, ...values } : m)),
    });
  return (
    <Panel
      title="多站点联动"
      className="composer-content"
      action={
        <Button
          icon={Plus}
          disabled={!sites.length || mounts.length >= 16}
          onClick={() =>
            patch({
              site_mounts: [
                ...mounts,
                {
                  id: `site-${crypto.randomUUID().slice(0, 8)}`,
                  name: "新子站点",
                  site_id:
                    sites.find((s) => s.id !== workspace.site_id)?.id ||
                    sites[0].id,
                  parent_id: "root",
                  segment: `site-${mounts.length + 1}`,
                  server_header: "",
                },
              ],
            })
          }
        >
          添加子站点
        </Button>
      }
    >
      <div className="workspace-site-list">
        <p className="muted">
          每个子站点单独选用素材，可继续嵌套。保存工作区后同步已绑定端口；场景在“绑定提示词”中选择所属站点。发布并保存当前改动后，可将任一子站点绑定到独立端口的
          /。
        </p>
        {nodes.map((node) => (
          <section
            className="workspace-site-node"
            key={node.id}
            aria-label={`${node.name}站点配置`}
          >
            {node.id === "root" ? (
              <strong>
                主站点 ·{" "}
                {sites.find((s) => s.id === workspace.site_id)?.name ||
                  "请先选择素材"}
              </strong>
            ) : (
              <div className="form-grid">
                <Field label="站点名称">
                  <input
                    aria-label={`${node.id}站点名称`}
                    value={node.name}
                    onChange={(e) => update(node.id, { name: e.target.value })}
                  />
                </Field>
                <Field label="站点素材">
                  <Select
                    aria-label={`${node.id}站点素材`}
                    value={node.site_id}
                    onChange={(e) =>
                      update(node.id, { site_id: e.target.value })
                    }
                  >
                    {sites.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name} · v{s.version}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field label="上级站点">
                  <Select
                    aria-label={`${node.id}上级站点`}
                    value={node.parent_id || "root"}
                    onChange={(e) =>
                      update(node.id, { parent_id: e.target.value })
                    }
                  >
                    {nodes
                      .filter((n) => n.id !== node.id)
                      .map((n) => (
                        <option key={n.id} value={n.id}>
                          {n.name}
                        </option>
                      ))}
                  </Select>
                </Field>
                <Field label="路径段">
                  <input
                    aria-label={`${node.id}路径段`}
                    value={node.segment}
                    placeholder="oss"
                    onChange={(e) =>
                      update(node.id, { segment: e.target.value })
                    }
                  />
                </Field>
                <Field label="Server 响应头（可选）">
                  <input
                    value={node.server_header || ""}
                    placeholder="留空继承工作区"
                    onChange={(e) =>
                      update(node.id, { server_header: e.target.value })
                    }
                  />
                </Field>
              </div>
            )}
            <div className="workspace-site-link">
              <code>{siteEntryLink(workspace, node.id, listeners)}</code>
              <CopyButton
                value={siteEntryLink(workspace, node.id, listeners)}
                label="复制站点链接"
                notify={notify}
              />
              <Button
                icon={FileCode2}
                disabled={!node.site_id}
                onClick={() => onEdit(sites.find((s) => s.id === node.site_id))}
              >
                编辑本站素材
              </Button>
              <Button disabled={!canBind} onClick={() => onBind(node.id)}>
                绑定独立端口
              </Button>
              {node.id !== "root" && (
                <Button
                  icon={Trash2}
                  disabled={
                    mounts.some((m) => m.parent_id === node.id) ||
                    workspace.bindings.some(
                      (b) => b.site_node_id === node.id,
                    ) ||
                    listeners.some((l) => l.site_node_id === node.id)
                  }
                  onClick={() =>
                    patch({
                      site_mounts: mounts.filter((m) => m.id !== node.id),
                    })
                  }
                >
                  移除子站点
                </Button>
              )}
            </div>
          </section>
        ))}
        <p className="muted">
          复制站点链接到上级页面即可跳转；绑定独立端口后，链接会优先使用该端口。
          资源和接口可使用相对路径（如 css/style.css、api/login）；带 /
          开头的链接指向当前端口的首页站点。移除站点前先移除其下级、场景绑定及端口绑定。
        </p>
      </div>
    </Panel>
  );
}

export function HostedFiles({
  workspace,
  sites,
  refresh,
  notify,
  dirty = false,
  listeners = [],
}) {
  const [nodeID, setNodeID] = useState("root");
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false),
    [progress, setProgress] = useState(0),
    [error, setError] = useState("");
  const nodes = workspaceSiteNodes(workspace);
  const node = nodes.find((n) => n.id === nodeID) || nodes[0];
  const site = sites.find((s) => s.id === node.site_id);
  const files = site?.files.filter((f) => f.encoding === "hosted") || [];
  const upload = async (file) => {
    if (!file || !workspace.id || dirty) return;
    const target = path.trim() || file.name;
    setError("");
    if (file.size > 128 * 1024 * 1024) {
      setError("单个文件不得超过 128 MiB");
      return;
    }
    if (site?.files.some((f) => f.path === target)) {
      setError("该路径已有文件，请改用新路径或先移除原文件");
      return;
    }
    setBusy(true);
    setProgress(0);
    try {
      const asset = await uploadHostedFile(file, setProgress);
      await api(`/workspaces/${workspace.id}/hosted-files`, "POST", {
        version: workspace.version,
        site_version: site?.version || 0,
        site_node_id: node.id,
        site_id: node.site_id || "",
        path: target,
        file_id: asset.id,
        download_name: file.name,
      });
      await refresh();
      setPath("");
      notify("文件已托管，已发布站点的下载链接已同步");
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  };
  const remove = async (file) => {
    setBusy(true);
    setError("");
    try {
      await api("/sites", "POST", {
        ...site,
        files: site.files.filter((f) => f.path !== file.path),
      });
      await refresh();
      notify("已移除当前下载链接，历史文件版本保留");
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Panel title="文件托管与 HTTP 下载" className="composer-content">
      <div className="workspace-site-list">
        <div className="form-grid">
          <Field label="下载所属站点">
            <Select
              aria-label="下载所属站点"
              value={node.id}
              disabled={busy}
              onChange={(e) => setNodeID(e.target.value)}
            >
              {nodes.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.name} · {n.mount_path}
                </option>
              ))}
            </Select>
          </Field>
          <Field
            label="站内下载路径"
            hint="留空使用上传文件名。例如 downloads/vpn.zip。"
          >
            <input
              aria-label="站内下载路径"
              disabled={busy}
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="downloads/vpn.zip"
            />
          </Field>
        </div>
        <label className="hosted-upload">
          <Upload size={17} />
          <span>
            {busy
              ? `正在上传或保存 ${progress}%`
              : "选择并托管文件（最大 128 MiB）"}
          </span>
          <input
            type="file"
            aria-label="上传托管文件"
            disabled={busy || !workspace.id || dirty}
            onChange={(e) => {
              const file = e.target.files[0];
              e.target.value = "";
              upload(file);
            }}
          />
        </label>
        {dirty && <p className="muted">请先保存工作区的修改，再上传文件。</p>}
        {!dirty && !site && (
          <p className="muted">无需先创建主站，上传后会自动建立下载页。</p>
        )}
        {error && (
          <p className="composer-error" role="alert">
            {error}
          </p>
        )}
        {files.map((file) => (
          <div className="workspace-site-link" key={file.path}>
            <strong>{file.download_name || file.path}</strong>
            <code>
              {siteEntryLink(workspace, node.id, listeners)}
              {file.path}
            </code>
            <CopyButton
              value={
                siteEntryLink(workspace, node.id, listeners) +
                file.path.split("/").map(encodeURIComponent).join("/")
              }
              label="复制下载链接"
              notify={notify}
            />
            <Button icon={Trash2} disabled={busy} onClick={() => remove(file)}>
              移除下载
            </Button>
          </div>
        ))}
        <p className="muted">
          文件以附件形式通过 HTTP 公开下载，支持 HEAD 和
          Range。上传会保存到所选素材并同步引用它的已发布工作区；首次发布仍需绑定端口。新增子站点需保存工作区后生效。
        </p>
      </div>
    </Panel>
  );
}
