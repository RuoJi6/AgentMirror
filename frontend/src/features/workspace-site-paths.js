export function workspaceSiteNodes(workspace) {
  const mounts = workspace.site_mounts || [];
  const byID = new Map(mounts.map((m) => [m.id, m]));
  const path = (node, seen = new Set()) => {
    if (!node || seen.has(node.id)) return "（上级无效）/";
    seen.add(node.id);
    return (
      (node.parent_id === "root" || !node.parent_id
        ? "/"
        : path(byID.get(node.parent_id), seen)) +
      (node.segment || "未命名") +
      "/"
    );
  };
  return [
    { id: "root", name: "主站点", site_id: workspace.site_id, mount_path: "/" },
    ...mounts.map((m) => ({ ...m, mount_path: path(m) })),
  ];
}

export function siteEntryLink(workspace, nodeId, listeners = []) {
  const nodes = workspaceSiteNodes(workspace),
    node = nodes.find((n) => n.id === nodeId);
  if (!node) return "";
  const choices = listeners
    .filter((l) => l.enabled)
    .map((l) => ({
      listener: l,
      root: nodes.find((n) => n.id === (l.site_node_id || "root")),
    }))
    .filter(
      ({ root }) =>
        root &&
        (root.id === node.id || node.mount_path.startsWith(root.mount_path)),
    )
    .sort((a, b) => b.root.mount_path.length - a.root.mount_path.length);
  if (!choices.length) return node.mount_path;
  const { listener, root } = choices[0];
  return (
    listener.public_url.replace(/\/$/, "") +
    "/" +
    node.mount_path.slice(root.mount_path.length)
  );
}
export function siteNodeName(workspace, nodeId) {
  return (
    workspaceSiteNodes(workspace).find((n) => n.id === (nodeId || "root"))
      ?.name || "站点已移除"
  );
}
