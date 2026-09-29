import React, { useEffect, useMemo, useRef, useState } from "react";
import { ArrowRightLeft, House, Play } from "lucide-react";
import { api } from "../api";
import Select from "../components/Select";
import {
  Badge,
  Button,
  Callout,
  Field,
  JsonView,
  Panel,
} from "../components/UI";
import HeaderFields from "./ComposerHeaders";
import ScenarioEndpoints from "./ScenarioEndpoints";
import { workspaceSiteNodes } from "./workspace-site-paths";
import {
  previewExamples,
  previewRuleKey,
  previewRules,
} from "./preview-requests";
import "./request-preview.css";

const methods = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
const homeRequest = () => ({ method: "GET", path: "/", headers: {}, body: "" });

function PreviewResult({ entry, rules }) {
  if (entry.error)
    return (
      <section className="composer-result">
        <h3>{entry.label}</h3>
        <p role="alert" className="composer-error">
          {entry.error}
        </p>
      </section>
    );
  const result = entry.result;
  const matched = rules.find(
    (r) => r.bindingId === result.binding_id && r.ruleId === result.rule_id,
  );
  const different =
    result.rule_matched && entry.targetKey && matched?.key !== entry.targetKey;
  return (
    <section
      className="composer-result preview-response"
      aria-label={entry.label}
    >
      <div className="composer-section-line">
        <h3>{entry.label}</h3>
        <div className="actions">
          <Badge tone={result.status < 400 ? "green" : "amber"}>
            HTTP {result.status}
          </Badge>
          <Badge>
            {result.fallback
              ? "普通返回"
              : result.rule_matched
                ? "触发条件命中"
                : result.matched
                  ? "站点响应"
                  : "未匹配规则"}
          </Badge>
          <Badge tone={result.deliveries?.length ? "green" : ""}>
            {result.deliveries?.length
              ? `提示词已交付 · ${result.deliveries.length} 处`
              : "未交付提示词"}
          </Badge>
        </div>
      </div>
      <code className="preview-sent-request">
        {entry.request.method} {entry.request.path}
      </code>
      {matched && (
        <small className="muted">
          {different ? "实际命中了其他规则：" : "响应规则："}
          {matched.label}
        </small>
      )}
      {result.download && (
        <Callout>
          托管下载：{result.download.name} · {result.download.size}{" "}
          字节。预演仅显示文件元数据；发布后可通过此路径下载。
          <code>SHA-256: {result.download.sha256}</code>
        </Callout>
      )}
      <pre className="composer-raw-response">
        {typeof result.body === "string"
          ? result.body
          : JSON.stringify(result.body, null, 2)}
      </pre>
      <details>
        <summary>响应头 · {result.content_type}</summary>
        <JsonView value={result.headers} />
      </details>
      <details>
        <summary>提示词交付位置与匹配轨迹</summary>
        <JsonView
          value={{ deliveries: result.deliveries, trace: result.trace }}
        />
      </details>
      {result.content_type?.includes("text/html") && (
        <details>
          <summary>HTML 渲染视图</summary>
          <iframe
            className="composer-site-preview"
            sandbox=""
            title="模拟响应 HTML 预览"
            srcDoc={typeof result.body === "string" ? result.body : ""}
          />
        </details>
      )}
    </section>
  );
}

export default function RequestPreview({
  workspace,
  scenarios,
  profiles,
  site,
  target,
  onNavigate,
  onUseScenario,
}) {
  const [entrySelection, setEntrySelection] = useState("root");
  const nodes = workspaceSiteNodes(workspace);
  const entryNodeId = nodes.some((n) => n.id === entrySelection)
    ? entrySelection
    : "root";
  const rules = useMemo(
    () =>
      previewRules(
        workspace.bindings,
        scenarios,
        workspace.site_mounts,
        entryNodeId,
      ),
    [workspace.bindings, scenarios, workspace.site_mounts, entryNodeId],
  );
  const contextKey = useMemo(
    () =>
      JSON.stringify([
        workspace.id,
        workspace.site_id,
        workspace.site_mounts,
        entryNodeId,
        site?.version,
        workspace.bindings,
        scenarios.map((s) => [s.id, s.version]),
        profiles
          .filter((p) =>
            workspace.bindings.some(
              (b) =>
                b.profile_id === p.id ||
                Object.values(b.rule_profiles || {}).includes(p.id),
            ),
          )
          .map((p) => [p.id, p.version]),
      ]),
    [
      workspace.id,
      workspace.site_id,
      workspace.site_mounts,
      entryNodeId,
      site?.version,
      workspace.bindings,
      scenarios,
      profiles,
    ],
  );
  const [selectedKey, setSelectedKey] = useState("");
  const [request, setRequest] = useState(homeRequest);
  const [edited, setEdited] = useState(false);
  const [entries, setEntries] = useState([]);
  const [busy, setBusy] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const epoch = useRef(0);
  const inFlight = useRef(false);
  const lastTarget = useRef(null);
  const selected = rules.find((r) => r.key === selectedKey);
  const examples = useMemo(
    () => (selected ? previewExamples(selected.rule) : null),
    [selected],
  );
  const missingProfile = rules.some((r) => r.needsProfile);
  const available = !!site || rules.length > 0;
  const canSend =
    available && !missingProfile && !busy && request.path.startsWith("/");

  const loadExample = (item) => {
    const example = item ? previewExamples(item.rule) : null;
    epoch.current++;
    setSelectedKey(item?.key || "");
    setRequest(example?.request || homeRequest());
    setEdited(false);
    setAdvanced(!!example?.notes.length);
    setEntries([]);
  };

  useEffect(() => {
    const requestedKey =
      target &&
      target !== lastTarget.current &&
      previewRuleKey(target.bindingId, target.ruleId);
    lastTarget.current = target;
    const item =
      rules.find((r) => r.key === requestedKey) ||
      rules.find((r) => r.key === selectedKey) ||
      rules[0];
    loadExample(item);
    // The serialized context resets stale responses when bindings/materials
    // change, but does not reset hand edits on an unrelated parent render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contextKey, target]);
  useEffect(
    () => () => {
      epoch.current++;
    },
    [],
  );

  const patchRequest = (patch) => {
    epoch.current++;
    setRequest((r) => ({ ...r, ...patch }));
    setEdited(true);
    setEntries([]);
  };
  const send = async (compare = false) => {
    if (!canSend || inFlight.current) return;
    inFlight.current = true;
    const version = ++epoch.current;
    setBusy(compare ? "compare" : "send");
    setEntries([]);
    const requests = compare
      ? [
          { label: "条件示例", request: examples.request },
          { label: "空参数对照", request: examples.control },
        ]
      : [{ label: "实际返回", request }];
    try {
      const results = await Promise.all(
        requests.map(async (entry) => {
          try {
            const result = await api("/composer/preview", "POST", {
              site_id: workspace.site_id,
              site_mounts: workspace.site_mounts || [],
              site_node_id: entryNodeId,
              bindings: workspace.bindings,
              callback_path: workspace.callback_path || "/collect",
              server_header: workspace.server_header || "",
              request: entry.request,
            });
            return { ...entry, targetKey: selectedKey, result };
          } catch (error) {
            return { ...entry, error: error.message };
          }
        }),
      );
      if (version === epoch.current) setEntries(results);
    } finally {
      inFlight.current = false;
      setBusy("");
    }
  };

  return (
    <Panel title="请求预演" className="composer-content preview-panel">
      <div className="preview-intro">
        <p>
          从模拟场景选择接口路径和参数，预演实际返回。无需发布，也不会创建正式会话。
        </p>
        {site && (
          <Button
            icon={House}
            variant="ghost"
            disabled={!!busy}
            onClick={() => loadExample(null)}
          >
            站点首页
          </Button>
        )}
      </div>
      {nodes.length > 1 && (
        <Field label="预演站点入口">
          <Select
            value={entryNodeId}
            disabled={!!busy}
            onChange={(e) => setEntrySelection(e.target.value)}
          >
            {nodes.map((node) => (
              <option key={node.id} value={node.id}>
                {node.name} · 作为端口首页
              </option>
            ))}
          </Select>
        </Field>
      )}
      <ScenarioEndpoints
        bindings={workspace.bindings}
        siteMounts={workspace.site_mounts}
        entryNodeId={entryNodeId}
        scenarios={scenarios}
        profiles={profiles}
        selectedKey={selectedKey}
        busy={!!busy}
        onSelect={(key) => loadExample(rules.find((r) => r.key === key))}
        onUseScenario={onUseScenario}
        onNavigate={onNavigate}
      />
      {!!scenarios.length && !workspace.bindings.length && (
        <Callout>
          当前工作区尚未加入模拟场景。上方已列出场景库接口，点击「添加并填入」即可使用；当前的
          / 仅请求站点首页。
        </Callout>
      )}
      {!available ? (
        <Callout>
          尚无可预演的内容。
          <Button variant="ghost" onClick={() => onNavigate("scenarios")}>
            添加模拟场景
          </Button>
          <Button variant="ghost" onClick={() => onNavigate("site")}>
            选择站点素材
          </Button>
        </Callout>
      ) : null}
      {missingProfile && (
        <Callout>
          交付规则尚未指定提示词，请先完成绑定。
          <Button variant="ghost" onClick={() => onNavigate("bindings")}>
            绑定提示词
          </Button>
        </Callout>
      )}
      <div
        className="preview-controls"
        onKeyDown={(event) => {
          if (
            (event.ctrlKey || event.metaKey) &&
            event.key === "Enter" &&
            !event.nativeEvent.isComposing
          ) {
            event.preventDefault();
            send();
          }
        }}
      >
        {selected && (
          <div className="preview-selected-rule">
            <span>当前规则：{selected.label}</span>
            <Button
              disabled={!!busy}
              variant="ghost"
              onClick={() => loadExample(selected)}
            >
              重新填入
            </Button>
          </div>
        )}
        <div className="composer-request-line">
          <Field label="预演方法">
            <Select
              value={request.method}
              disabled={!!busy}
              onChange={(e) => patchRequest({ method: e.target.value })}
            >
              {methods.map((method) => (
                <option key={method} value={method}>
                  {method}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="预演路径（可含查询参数）">
            <input
              disabled={!!busy}
              value={request.path}
              onChange={(e) => patchRequest({ path: e.target.value })}
            />
          </Field>
          <Button
            icon={Play}
            variant="primary"
            disabled={!canSend}
            loading={busy === "send"}
            onClick={() => send()}
          >
            发送预演请求
          </Button>
        </div>
        <div className="preview-shortcuts">
          <span className="muted">
            {edited
              ? "已手动调整"
              : selected
                ? `已填入 ${selected.rule.conditions?.length || 0} 个条件`
                : "可直接输入自定义路径"}{" "}
            · ⌘ / Ctrl + Enter 发送
          </span>
          {examples?.control && (
            <Button
              icon={ArrowRightLeft}
              disabled={!canSend || edited}
              loading={busy === "compare"}
              onClick={() => send(true)}
            >
              对比条件与空参数
            </Button>
          )}
        </div>
        {edited && examples?.control && (
          <small className="muted">
            重新填入规则示例后可进行对比；手动请求使用「发送预演请求」。
          </small>
        )}
        {examples?.notes.map((note) => (
          <Callout key={note}>{note}</Callout>
        ))}
        <details
          className="preview-advanced"
          open={advanced}
          onToggle={(e) => setAdvanced(e.currentTarget.open)}
        >
          <summary>
            高级参数{" "}
            <span>
              {Object.keys(request.headers).length} 个请求头 ·{" "}
              {request.body ? "含请求正文" : "无正文"}
            </span>
          </summary>
          <fieldset disabled={!!busy}>
            <HeaderFields
              label="请求头"
              value={request.headers}
              onChange={(headers) => patchRequest({ headers })}
            />
            <Field label="请求正文">
              <textarea
                className="composer-code request-code"
                rows={4}
                value={request.body}
                onChange={(e) => patchRequest({ body: e.target.value })}
                placeholder='{"q":"测试标记"}'
              />
            </Field>
          </fieldset>
        </details>
      </div>
      {!!entries.length && (
        <div
          className={`preview-results ${entries.length > 1 ? "comparison" : ""}`}
          aria-live="polite"
        >
          {entries.length > 1 && (
            <p className="preview-comparison-note muted">
              同一路径分别发送规则示例和空参数请求，按实际匹配结果对比；空参数也可能命中其他规则。
            </p>
          )}
          {entries.map((entry) => (
            <PreviewResult key={entry.label} entry={entry} rules={rules} />
          ))}
          <small className="preview-comparison-note muted">
            响应含提示词只证明返回内容，模型是否读取或执行需结合 Agent
            日志与回传。HEAD 请求不记作交付。
          </small>
        </div>
      )}
    </Panel>
  );
}
