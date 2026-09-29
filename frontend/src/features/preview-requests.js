import { workspaceSiteNodes } from "./workspace-site-paths.js";
// These are editable examples, not an alternative rule engine. The preview API
// remains authoritative for matching, fallback order and prompt delivery.
export const previewRuleKey = (bindingId, ruleId) =>
  JSON.stringify([bindingId, ruleId]);

export const bindingProfileId = (binding, ruleId) =>
  binding?.rule_profiles?.[ruleId] || binding?.profile_id || "";

function mountedPath(binding, rule, mounts, entryNodeId = "root") {
  const nodes = workspaceSiteNodes({ site_mounts: mounts });
  const entry = nodes.find((n) => n.id === entryNodeId);
  const node = nodes.find((n) => n.id === (binding?.site_node_id || "root"));
  if (!entry || !node?.mount_path.startsWith(entry.mount_path)) return null;
  return (
    ("/" + node.mount_path.slice(entry.mount_path.length)).replace(/\/$/, "") +
    (binding?.paths?.[rule.id] || rule.path)
  );
}

export function previewRules(
  bindings,
  scenarios,
  mounts = [],
  entryNodeId = "root",
) {
  const library = new Map(scenarios.map((s) => [s.id, s]));
  return bindings.flatMap((binding, index) => {
    const scenario = library.get(binding.scenario_id);
    if (binding.enabled === false || !scenario) return [];
    return scenario.rules
      .map((rule) => ({
        key: previewRuleKey(binding.id, rule.id),
        bindingId: binding.id,
        ruleId: rule.id,
        label: `${scenario.name} · ${rule.name || rule.path} · 实例 ${index + 1}`,
        rule: {
          ...rule,
          path: mountedPath(binding, rule, mounts, entryNodeId),
        },
        needsProfile:
          rule.delivery_required && !bindingProfileId(binding, rule.id),
      }))
      .filter((item) => item.rule.path !== null);
  });
}

// The endpoint catalogue includes saved materials even before they are bound.
// Only previewRules above may be treated as active routes by the request UI.
export function scenarioEndpoints(
  bindings,
  scenarios,
  mounts = [],
  entryNodeId = "root",
) {
  return scenarios.flatMap((scenario) => {
    const instances = bindings.filter((b) => b.scenario_id === scenario.id);
    return (instances.length ? instances : [null]).flatMap((binding) =>
      scenario.rules
        .map((rule) => ({
          key: binding
            ? previewRuleKey(binding.id, rule.id)
            : JSON.stringify(["library", scenario.id, rule.id]),
          scenario,
          binding,
          originalPath: rule.path,
          rule: {
            ...rule,
            path: mountedPath(binding, rule, mounts, entryNodeId),
          },
          needsProfile: scenario.rules.some(
            (r) => r.delivery_required && !bindingProfileId(binding, r.id),
          ),
          instanceNumber: binding
            ? bindings.findIndex((b) => b.id === binding.id) + 1
            : null,
        }))
        .filter((item) => item.rule.path !== null),
    );
  });
}

export function conditionLabel(condition) {
  const sources = {
    query: "查询",
    header: "请求头",
    form: "表单",
    json: "JSON",
    body: "正文",
  };
  const field = `${sources[condition.source] || condition.source}${condition.key ? ` · ${condition.key}` : ""}`;
  return condition.operator === "exists"
    ? `${field}（必填）`
    : `${field} ${condition.operator === "contains" ? "包含" : "="} ${JSON.stringify(condition.value ?? "")}`;
}

const partsOf = (key) =>
  key.startsWith("/")
    ? key
        .slice(1)
        .split("/")
        .map((p) => p.replaceAll("~1", "/").replaceAll("~0", "~"))
    : key.split(".");

function setJSON(root, parts, value) {
  let node = root;
  for (let i = 0; i < parts.length; i++) {
    const part = parts[i];
    const key = Array.isArray(node) ? Number(part) : part;
    if (Array.isArray(node) && (!/^\d+$/.test(part) || Number(key) > 100))
      throw new Error("数组下标需要手动填写");
    if (i === parts.length - 1) {
      if (Object.hasOwn(node, key) && node[key] !== value)
        throw new Error("JSON 字段条件重叠");
      node[key] = value;
    } else {
      if (!Object.hasOwn(node, key))
        node[key] = /^\d+$/.test(parts[i + 1]) ? [] : Object.create(null);
      node = node[key];
      if (node === null || typeof node !== "object")
        throw new Error("JSON 字段条件重叠");
    }
  }
}

function exampleValue(conditions, notes) {
  const equals = conditions.filter((c) => c.operator === "equals");
  const contains = conditions.filter((c) => c.operator === "contains");
  const value = equals.length
    ? String(equals[0].value ?? "")
    : contains.map((c) => c.value ?? "").join(" ") || "preview";
  if (
    equals.some((c) => String(c.value ?? "") !== value) ||
    contains.some((c) => !value.includes(String(c.value ?? "")))
  )
    notes.add("部分条件无法合并为一个值，请在高级参数中调整，或检查场景条件。");
  return value;
}

function buildRequest(rule, conditions, notes) {
  const query = new URLSearchParams();
  const form = new URLSearchParams();
  const headers = Object.create(null);
  const groups = new Map();
  for (const c of conditions) {
    const key = c.source === "header" ? c.key.toLowerCase() : c.key || "";
    const id = JSON.stringify([c.source, key]);
    if (!groups.has(id))
      groups.set(id, { source: c.source, key, conditions: [] });
    groups.get(id).conditions.push(c);
  }
  let json;
  let rawBody;
  for (const group of groups.values()) {
    const { source, key, conditions: items } = group;
    if (source === "query" || source === "form") {
      // Repeated parameters can satisfy separate equals/contains constraints.
      const values = [
        ...new Set(
          items.map((c) =>
            c.operator === "exists" ? "preview" : String(c.value ?? ""),
          ),
        ),
      ];
      for (const value of values)
        (source === "query" ? query : form).append(key, value);
      continue;
    }
    const value = exampleValue(items, notes);
    if (source === "header") headers[key] = value;
    else if (source === "body") rawBody = value;
    else if (source === "json") {
      const parts = partsOf(key);
      json ??= /^\d+$/.test(parts[0]) ? [] : Object.create(null);
      try {
        setJSON(json, parts, value);
      } catch {
        notes.add("JSON 字段条件重叠或数组下标过大，请手动检查请求正文。");
      }
    }
  }
  let body = "";
  if (json !== undefined) {
    body = JSON.stringify(json, null, 2);
    headers["content-type"] ??= "application/json";
  }
  if (form.size) {
    if (json !== undefined)
      notes.add("规则同时要求 JSON 和表单，请手动检查正文格式。");
    body = form.toString();
    headers["content-type"] ??= "application/x-www-form-urlencoded";
    if (
      !headers["content-type"]
        .toLowerCase()
        .startsWith("application/x-www-form-urlencoded")
    )
      notes.add("表单示例使用 URL 编码，请手动调整 Content-Type 或请求正文。");
  }
  if (rawBody !== undefined) {
    if (body && body !== rawBody)
      notes.add("完整正文条件与字段条件并存，请手动检查是否同时满足。");
    body = rawBody;
  }
  // Encode Unicode in authored paths without changing path separators.
  const path = rule.path.split("/").map(encodeURIComponent).join("/");
  return {
    method: rule.method,
    path: path + (query.size ? `?${query}` : ""),
    headers,
    body,
  };
}

export function previewExamples(rule) {
  const notes = new Set();
  const conditions = rule.conditions || [];
  const request = buildRequest(rule, conditions, notes);
  // The control deliberately has no parameters. It may hit another rule with
  // the same route; the UI reports the actual response instead of assuming 404.
  const control = conditions.length ? buildRequest(rule, [], new Set()) : null;
  return { request, control, notes: [...notes] };
}

export function previewRequestText(rule) {
  const { request } = previewExamples(rule);
  const headers = Object.entries(request.headers).map(
    ([name, value]) => `${name}: ${value}`,
  );
  return (
    [`${request.method} ${request.path}`, ...headers].join("\n") +
    (request.body ? `\n\n${request.body}` : "")
  );
}
