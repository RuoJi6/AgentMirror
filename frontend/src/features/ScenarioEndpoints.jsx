import React, { useMemo, useState } from "react";
import { Search, ArrowDownToLine, Plus, Power } from "lucide-react";
import { Badge, Button, Field, Modal } from "../components/UI";
import Select from "../components/Select";
import { conditionLabel, scenarioEndpoints } from "./preview-requests";

export default function ScenarioEndpoints({
  bindings,
  siteMounts,
  entryNodeId = "root",
  scenarios,
  profiles,
  selectedKey,
  busy,
  onSelect,
  onUseScenario,
  onNavigate,
}) {
  const [search, setSearch] = useState("");
  const [scenarioFilter, setScenarioFilter] = useState("");
  const [limit, setLimit] = useState(30);
  const [pending, setPending] = useState(null);
  const [profileId, setProfileId] = useState("");
  const endpoints = useMemo(
    () =>
      scenarioEndpoints(bindings, scenarios, siteMounts, entryNodeId).sort(
        (a, b) => {
          const rank = (item) =>
            !item.binding ? 2 : item.binding.enabled === false ? 1 : 0;
          return rank(a) - rank(b);
        },
      ),
    [bindings, scenarios, siteMounts, entryNodeId],
  );
  const query = search.trim().toLowerCase();
  const filtered = endpoints.filter(
    (item) =>
      (!scenarios.some((scenario) => scenario.id === scenarioFilter) ||
        item.scenario.id === scenarioFilter) &&
      [
        item.rule.method,
        item.rule.path,
        item.originalPath,
        item.rule.name,
        item.scenario.name,
        ...item.rule.conditions.map(conditionLabel),
      ]
        .join(" ")
        .toLowerCase()
        .includes(query),
  );
  const visibleGroups = [
    ...new Set(filtered.slice(0, limit).map((item) => item.scenario.id)),
  ].map((id) => ({
    id,
    name: scenarios.find((scenario) => scenario.id === id)?.name || "模拟场景",
    items: filtered.slice(0, limit).filter((item) => item.scenario.id === id),
  }));
  const needsProfile = pending?.scenario.rules.some((r) => r.delivery_required);
  const select = (item) => {
    if (item.binding?.enabled !== false && item.binding && !item.needsProfile)
      onSelect(item.key);
    else if (!item.needsProfile)
      onUseScenario(item, item.binding?.profile_id || "");
    else {
      setPending(item);
      setProfileId(item.binding?.profile_id || "");
    }
  };
  return (
    <section className="preview-endpoints" aria-label="模拟场景接口">
      <div className="preview-endpoints-heading">
        <div>
          <h3>
            模拟场景接口 <span>{endpoints.length}</span>
          </h3>
          <p>
            {entryNodeId === "root"
              ? "直接选择路径和参数；未加入工作区的场景可在这里添加。"
              : "显示所选站点及其子站接口，路径与独立端口一致。"}
          </p>
        </div>
        <label className="preview-endpoint-search">
          <Search size={16} />
          <input
            aria-label="搜索场景路径或参数"
            placeholder="搜索路径、参数、场景…"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setLimit(30);
            }}
          />
        </label>
      </div>
      <div className="preview-scenario-filter">
        <Field label="按场景筛选">
          <Select
            value={
              scenarios.some((scenario) => scenario.id === scenarioFilter)
                ? scenarioFilter
                : ""
            }
            onChange={(event) => {
              setScenarioFilter(event.target.value);
              setLimit(30);
            }}
          >
            <option value="">全部场景（{scenarios.length}）</option>
            {scenarios.map((scenario) => (
              <option key={scenario.id} value={scenario.id}>
                {scenario.name}
              </option>
            ))}
          </Select>
        </Field>
        <span className="muted">
          {filtered.length} 条接口 · 同一场景集中显示
        </span>
      </div>
      {!endpoints.length ? (
        <div className="preview-endpoints-empty">
          还没有模拟场景。
          <Button variant="ghost" onClick={() => onNavigate("scenarios")}>
            创建或选择预设场景
          </Button>
        </div>
      ) : !filtered.length ? (
        <p className="preview-endpoints-empty">
          没有匹配的接口，请尝试其他路径或参数名。
        </p>
      ) : (
        <div className="preview-endpoint-list">
          {visibleGroups.map((group) => (
            <section
              key={group.id}
              className="preview-scenario-group"
              aria-label={group.name}
            >
              <h4>
                {group.name} <span>{group.items.length} 条接口</span>
              </h4>
              {group.items.map((item) => {
                const enabled = item.binding && item.binding.enabled !== false;
                const status = !item.binding
                  ? "未加入工作区"
                  : !enabled
                    ? "已停用"
                    : item.needsProfile
                      ? "待绑定提示词"
                      : "已加入工作区";
                return (
                  <button
                    key={item.key}
                    type="button"
                    data-endpoint-key={item.key}
                    className={`preview-endpoint ${item.key === selectedKey ? "selected" : ""}`}
                    aria-pressed={item.key === selectedKey}
                    disabled={busy}
                    onClick={() => select(item)}
                  >
                    <div className="preview-endpoint-main">
                      <div className="preview-endpoint-route">
                        <Badge>{item.rule.method}</Badge>
                        <code>{item.rule.path}</code>
                      </div>
                      <small>
                        {item.scenario.name} · {item.rule.name || "未命名规则"}
                        {item.instanceNumber
                          ? ` · 实例 ${item.instanceNumber}`
                          : ""}
                      </small>
                      {item.originalPath !== item.rule.path && (
                        <small>
                          场景原路径：{item.originalPath} → 已使用工作区映射
                        </small>
                      )}
                      <div className="preview-endpoint-params">
                        {item.rule.conditions.length ? (
                          item.rule.conditions.map((c, index) => (
                            <code key={index}>{conditionLabel(c)}</code>
                          ))
                        ) : (
                          <span>无附加参数条件</span>
                        )}
                      </div>
                    </div>
                    <div className="preview-endpoint-action">
                      <span>{status}</span>
                      <strong>
                        {!item.binding ? (
                          <Plus size={14} />
                        ) : !enabled ? (
                          <Power size={14} />
                        ) : (
                          <ArrowDownToLine size={14} />
                        )}
                        {!item.binding
                          ? "添加并填入"
                          : !enabled
                            ? "启用并填入"
                            : item.needsProfile
                              ? "绑定并填入"
                              : "填入请求"}
                      </strong>
                    </div>
                  </button>
                );
              })}
            </section>
          ))}
          {filtered.length > limit && (
            <Button variant="ghost" onClick={() => setLimit((n) => n + 30)}>
              显示更多接口（还有 {filtered.length - limit} 条）
            </Button>
          )}
        </div>
      )}
      {pending && (
        <Modal
          title="使用场景接口"
          onClose={() => setPending(null)}
          footer={
            <>
              <Button onClick={() => setPending(null)}>取消</Button>
              <Button
                variant="primary"
                disabled={
                  busy ||
                  (needsProfile && !profiles.some((p) => p.id === profileId))
                }
                onClick={() => {
                  onUseScenario(pending, profileId);
                  setPending(null);
                }}
              >
                {!pending.binding
                  ? "添加并填入"
                  : pending.binding.enabled === false
                    ? "启用并填入"
                    : "绑定并填入"}
              </Button>
            </>
          }
        >
          <div className="composer-stack">
            <p>
              将「{pending.scenario.name}」的 {pending.scenario.rules.length}{" "}
              条接口{pending.binding ? "用于" : "加入"}当前工作区草稿，并填入{" "}
              <code>
                {pending.rule.method} {pending.rule.path}
              </code>
              。
            </p>
            {needsProfile && (
              <Field label="此场景使用的提示词">
                <Select
                  value={profileId}
                  onChange={(e) => setProfileId(e.target.value)}
                >
                  <option value="">请选择提示词方案</option>
                  {profiles.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </Select>
              </Field>
            )}
            {needsProfile && !profiles.length && (
              <p>
                尚无提示词方案，请先在左侧「提示词方案」创建，再添加此场景。
              </p>
            )}
            <small className="muted">
              仅修改当前草稿，预演后可保存工作区；不会自动发布。
            </small>
          </div>
        </Modal>
      )}
    </section>
  );
}
