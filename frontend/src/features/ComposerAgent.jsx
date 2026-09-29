import React, { lazy, Suspense, useEffect, useRef, useState } from "react";
import {
  ArrowUp,
  Bot,
  ChevronRight,
  Check,
  Code2,
  Eye,
  Globe,
  History,
  LoaderCircle,
  MessageSquare,
  Plus,
  Settings2,
  Square,
} from "lucide-react";
import { api, formatTime } from "../api";
import { Button, Field, Panel } from "../components/UI";
import Select from "../components/Select";
import MentionInput, { ReferenceChips } from "./MentionInput";
import ConversationGroup from "./ConversationGroup";
import AgentActivity from "./AgentActivity";
import AgentTodo from "./AgentTodo";
import AgentQuestion from "./AgentQuestion";
import AgentAttachments, { AttachmentChips } from "./AgentAttachments";
import ReviewReport from "./ReviewReport";
const ChatMarkdown = lazy(() => import("../components/ChatMarkdown"));
const statusNames = {
  waiting_user: "等待你的回答",
  answered: "已回答",
  queued: "等待执行",
  running: "正在执行",
  completed: "结果已就绪",
  failed: "执行失败",
  cancelled: "已取消",
};
const active = (job) => job && ["queued", "running"].includes(job.status);
const resumeDetail = (turn, turns) => {
  const state = turn.resume_state;
  if (!state) return "";
  // Older jobs used the interruption label for normal question answers too.
  // Use the recorded answer relationship without changing historical records.
  if (
    !state.reason &&
    turns.some(
      (parent) =>
        parent.id === state.from_job_id &&
        parent.question?.answered_job_id === turn.id,
    )
  ) {
    return state.draft_restored
      ? "已收到回答，继续原任务；草稿、原始需求、计划和工具进度已保留。"
      : "已收到回答，继续原任务；原始需求、计划和工具进度已保留。";
  }
  return state.detail || "";
};
const textOf = (value) =>
  typeof value === "string" ? value : value ? JSON.stringify(value) : "";
const changedModules = (result) =>
  result?.kind === "message"
    ? []
    : Array.isArray(result?.changes)
      ? result.changes.filter((kind) => kind === "site" || kind === "scenario")
      : ["site", "scenario"].filter((kind) => !!result?.[kind]);
const hasDraftResult = (result) => changedModules(result).length > 0;
const isDraftTurn = (turn) =>
  turn.status === "completed" &&
  !["message", "review"].includes(turn.result?.kind) &&
  (!Array.isArray(turn.result?.changes) || turn.result.changes.length > 0);
const turnSummary = ({ result, ...turn }) => ({
  ...turn,
  summary: turn.summary || result?.summary || "",
  ...(result
    ? {
        result: {
          kind: result.kind,
          changes: result.changes,
          counts: result.counts,
          verdict: result.verdict,
        },
      }
    : {}),
});
const restoreDraftResult = async (full) => {
  const modules = ["site", "scenario"].filter(
    (kind) => !!full.materialized?.[`${kind}_id`],
  );
  if (!modules.length) return { full, issue: "" };
  const reads = await Promise.allSettled(
    modules.map((kind) =>
      api(
        `/${kind === "site" ? "sites" : "scenarios"}/${full.materialized[`${kind}_id`]}`,
      ),
    ),
  );
  if (
    reads.some(
      (read, index) =>
        read.status === "rejected" ||
        read.value?.id !== full.materialized[`${modules[index]}_id`],
    )
  )
    return {
      full,
      issue:
        "关联素材已删除或暂时无法读取，现显示原始生成结果。请继续调整生成新结果后采用。",
    };
  if (reads.some((read) => read.value.version !== 1))
    return {
      full,
      issue: "关联素材已变更，现显示原始生成结果。请继续调整生成新结果后采用。",
    };
  return {
    full: {
      ...full,
      result: {
        ...full.result,
        ...Object.fromEntries(
          modules.map((kind, index) => [kind, reads[index].value]),
        ),
      },
    },
    issue: "",
  };
};
const hydrateDraft = async (full, turns, completion) => {
  if (full.materialized) return restoreDraftResult(full);
  const draftIndex = turns.findIndex((turn) => turn.id === full.id);
  const completionIndex = turns.findIndex((turn) => turn.id === completion?.id);
  if (draftIndex < 0 || completionIndex < draftIndex || !completion?.result)
    return { full, issue: "" };
  const later = turns.slice(draftIndex + 1, completionIndex + 1);
  const requests = new Map([[completion.id, Promise.resolve(completion)]]);
  const preserved = await Promise.all(
    changedModules(full.result).map(async (kind) => {
      const boundary = later.findIndex(
        (turn) =>
          turn.status === "completed" &&
          !["message", "review"].includes(turn.result?.kind) &&
          (!Array.isArray(turn.result?.changes) ||
            turn.result.changes.includes(kind)),
      );
      // Only inherit edits from snapshots before the next authored version.
      const candidates = boundary < 0 ? later : later.slice(0, boundary);
      const source = [...candidates]
        .reverse()
        .find((turn) => turn.status === "completed");
      const sourceID = source?.id || (completion.id === full.id ? full.id : "");
      if (!sourceID) return null;
      if (!requests.has(sourceID))
        requests.set(sourceID, api(`/generation/jobs/${sourceID}`));
      try {
        const snapshot = await requests.get(sourceID);
        return snapshot.result?.[kind] ? [kind, snapshot.result[kind]] : null;
      } catch {
        return null;
      }
    }),
  );
  return {
    full: {
      ...full,
      result: {
        ...full.result,
        ...Object.fromEntries(preserved.filter(Boolean)),
      },
    },
    issue: "",
  };
};
const remember = (workspaceId, job) => {
  if (!workspaceId || job?.workspace_id !== workspaceId || !job.id) return;
  try {
    localStorage.setItem(`agentmirror.agent.v1.${workspaceId}`, job.id);
  } catch {}
};
const forget = (workspaceId) => {
  if (!workspaceId) return;
  try {
    localStorage.removeItem(`agentmirror.agent.v1.${workspaceId}`);
  } catch {}
};
const recalled = (workspaceId) => {
  if (!workspaceId) return null;
  try {
    return localStorage.getItem(`agentmirror.agent.v1.${workspaceId}`);
  } catch {
    return null;
  }
};
const saveTarget = (jobId, workspaceId, bindingId, adopted = false) => {
  if (!jobId || !workspaceId) return;
  try {
    localStorage.setItem(
      `agentmirror.agent-target.v1.${jobId}`,
      JSON.stringify({
        workspaceId,
        bindingId,
        adopted,
      }),
    );
  } catch {}
};
const readTarget = (jobId, workspaceId) => {
  if (!jobId || !workspaceId) return null;
  try {
    const value = JSON.parse(
      localStorage.getItem(`agentmirror.agent-target.v1.${jobId}`) || "null",
    );
    return value?.workspaceId === workspaceId ? value : null;
  } catch {
    return null;
  }
};

export default function ComposerAgent({
  workspaceId,
  workspaceName,
  site,
  sites,
  bindings,
  scenarios,
  profiles,
  model = {},
  modelError,
  onSettings,
  onReviewSettings,
  requestedJobId,
  onAdopt,
  onMaterialsChanged,
  renderSiteEditor,
  renderScenarioEditor,
}) {
  const [prompt, setPrompt] = useState("");
  const [references, setReferences] = useState([]);
  const [attachments, setAttachments] = useState([]);
  const [uploading, setUploading] = useState(false);
  const [editScope, setEditScope] = useState("auto");
  const [draftReferences, setDraftReferences] = useState([]);
  const [sourceURL, setSourceURL] = useState("");
  const [sourceOpen, setSourceOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [historyScope, setHistoryScope] = useState("current");
  const [bindingId, setBindingId] = useState("");
  const [job, setJob] = useState(null);
  const [conversations, setConversations] = useState([]);
  const [conversationID, setConversationID] = useState("");
  const [conversationOwner, setConversationOwner] = useState("");
  const [forceFork, setForceFork] = useState(false);
  const [turns, setTurns] = useState([]);
  const [truncated, setTruncated] = useState(false);
  const [draftJobID, setDraftJobID] = useState("");
  const [latestCompletedID, setLatestCompletedID] = useState("");
  const [context, setContext] = useState(null);
  const [result, setResult] = useState(null);
  const [adopted, setAdopted] = useState(false);
  const [materialized, setMaterialized] = useState(null);
  const [materialIssue, setMaterialIssue] = useState("");
  const [preview, setPreview] = useState(null);
  const [editor, setEditor] = useState(null);
  const [providers, setProviders] = useState([]);
  const [providerID, setProviderID] = useState("");
  const [agentID, setAgentID] = useState("writer");
  const [agents, setAgents] = useState([]);
  const [agentFilter, setAgentFilter] = useState("");
  useEffect(() => {
    let live = true;
    api("/agents")
      .then((data) => {
        if (live) setAgents(data.items.filter((a) => a.enabled));
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, []);
  const [defaultProviderID, setDefaultProviderID] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [historyError, setHistoryError] = useState("");
  const [providerError, setProviderError] = useState("");
  const input = useRef(null);
  const timelineRef = useRef(null);
  const followOutput = useRef(true);
  const providersRef = useRef([]);
  const selected = useRef(null);
  const loadedResult = useRef(null);
  const refreshedMaterials = useRef(new Set());
  const restored = useRef(false);
  const historyScopeRef = useRef("current");
  const viewRevision = useRef(0);
  const historyRevision = useRef(0);
  const conversationDrafts = useRef(new Map());
  const selectedAgent = agents.find((item) => item.id === agentID);
  const redteaming = selectedAgent?.role === "redteam";
  const reviewing = redteaming || selectedAgent?.role === "reviewer";
  const inheritedProviderID = selectedAgent?.provider_id || defaultProviderID;
  const provider = providers.find(
    (item) => item.id === (providerID || inheritedProviderID),
  );
  const canGenerate =
    !!selectedAgent &&
    (provider
      ? !!provider.base_url && !!provider.model
      : !!model.base_url && !!model.model);
  const pending = active(job);
  const waiting = job?.status === "waiting_user";
  const targetBinding = bindings.find((binding) => binding.id === bindingId);
  const scenario = scenarios.find(
    (item) => item.id === targetBinding?.scenario_id,
  );
  const hasDraft = !!draftJobID && hasDraftResult(result);
  const changes = changedModules(result);
  const changedSite = changes.includes("site") && !!result?.site;
  const changedScenario = changes.includes("scenario") && !!result?.scenario;
  const previewSite = changedSite ? result.site : null;
  const currentWorkspaceJob =
    !!workspaceId &&
    !forceFork &&
    conversationOwner === workspaceId &&
    job?.workspace_id === workspaceId;
  const historicalJob = !!job && !currentWorkspaceJob;
  const continuationID = job?.id || latestCompletedID;
  const continuing = !!continuationID && currentWorkspaceJob;
  const draftProfile = draftReferences.find((item) => item.kind === "profile");
  const timeline = job
    ? [...turns.filter((turn) => turn.id !== job.id), job]
    : turns;
  const latestPlan = timeline.findLast((turn) => turn.plan?.length)?.plan || [];
  const runningCount = conversations.reduce(
    (sum, group) => sum + group.running,
    0,
  );
  const conversationCount = conversations.reduce(
    (sum, group) => sum + group.total,
    0,
  );

  // Composer chips select identities for the next turn, not immutable versions.
  // History/draftReferences stay unchanged. Never replace a deleted ID by name,
  // or downgrade a newer server response while the library refresh is pending.
  useEffect(() => {
    const catalogues = { site: sites, scenario: scenarios, profile: profiles };
    setReferences((current) => {
      let changed = false;
      const next = current.map((ref) => {
        const latest = catalogues[ref.kind]?.find((item) => item.id === ref.id);
        if (!latest || latest.version < ref.version) return ref;
        if (latest.version === ref.version && latest.name === ref.name)
          return ref;
        changed = true;
        return { ...ref, version: latest.version, name: latest.name };
      });
      return changed ? next : current;
    });
  }, [sites, scenarios, profiles, references]);

  // Keep unsent input and local edits with their conversation while another
  // conversation runs. Persisted job state is always refreshed on selection.
  const stashConversation = () => {
    if (!conversationID) return;
    conversationDrafts.current.set(conversationID, {
      jobID: job?.id,
      prompt,
      attachments,
      references,
      editScope,
      sourceURL,
      sourceOpen,
      bindingId,
      providerID,
      agentID,
      latestCompletedID,
      draftJobID,
      result,
      context,
      draftReferences,
    });
  };

  const refreshConversations = async () => {
    const revision = ++historyRevision.current;
    if (!workspaceId) {
      setConversations([]);
      return [];
    }
    const scope = historyScopeRef.current;
    const filter = scope === "unassigned" ? "unassigned" : workspaceId;
    try {
      const next = await api(
        `/generation/conversations?summary=1&workspace_id=${encodeURIComponent(filter)}`,
      );
      const items = next.groups || [];
      if (historyRevision.current === revision) {
        setConversations(items);
        setHistoryError("");
      }
      return items;
    } catch (e) {
      if (historyRevision.current === revision) throw e;
      return [];
    }
  };
  const changeHistoryScope = (scope) => {
    restored.current = true;
    historyScopeRef.current = scope;
    historyRevision.current++;
    setHistoryScope(scope);
    setConversations([]);
    setHistoryError("");
  };
  const setSuccessfulDraft = (full, issue = "") => {
    setDraftJobID(full.id);
    setResult(structuredClone(full.result || {}));
    setDraftReferences(full.references || []);
    const saved = full.materialized;
    const profile = full.references?.find((item) => item.kind === "profile");
    setMaterialized(saved || null);
    setMaterialIssue(issue);
    setAdopted(
      !!(saved?.site_id || saved?.scenario_id) &&
        (!saved.site_id || site?.id === saved.site_id) &&
        (!saved.scenario_id ||
          bindings.some(
            (binding) =>
              binding.scenario_id === saved.scenario_id &&
              (!profile || binding.profile_id === profile.id),
          )),
    );
  };
  const loadJob = async (
    id,
    {
      restore = false,
      initial,
      fromHistory = historyScopeRef.current === "unassigned",
    } = {},
  ) => {
    if (uploading) {
      setError("附件正在上传，请完成或取消后切换对话。");
      return;
    }
    stashConversation();
    const revision = ++viewRevision.current;
    selected.current = id;
    setBusy("load");
    setError("");
    try {
      const full = initial || (await api(`/generation/jobs/${id}`));
      if (viewRevision.current !== revision) return;
      if (restore && (!workspaceId || full.workspace_id !== workspaceId)) {
        forget(workspaceId);
        selected.current = null;
        return;
      }
      const conversation = full.conversation_id
        ? await api(`/generation/conversations/${full.conversation_id}`)
        : { turns: [full], workspace_id: full.workspace_id || "" };
      const listed = conversation.turns?.length ? conversation.turns : [full];
      const latestID = listed.at(-1).id;
      const latest =
        latestID === full.id ? full : await api(`/generation/jobs/${latestID}`);
      const items = listed.map((turn) =>
        turn.id === latest.id ? latest : turn.id === full.id ? full : turn,
      );
      const owner = conversation.workspace_id || "";
      const mustFork =
        fromHistory ||
        owner !== workspaceId ||
        items.some((turn) => (turn.workspace_id || "") !== workspaceId);
      if (viewRevision.current !== revision) return;
      if (restore && mustFork) {
        forget(workspaceId);
        selected.current = null;
        return;
      }
      const newestFirst = [...items].reverse();
      const requests = new Map([
        [full.id, Promise.resolve(full)],
        [latest.id, Promise.resolve(latest)],
      ]);
      const readTurn = (turn) => {
        if (!turn) return Promise.resolve(null);
        if (!requests.has(turn.id))
          requests.set(turn.id, api(`/generation/jobs/${turn.id}`));
        return requests.get(turn.id);
      };
      const [completion, latestDraft] = await Promise.all([
        readTurn(newestFirst.find((turn) => turn.status === "completed")),
        readTurn(newestFirst.find(isDraftTurn)),
      ]);
      const restoredDraft =
        latestDraft && hasDraftResult(latestDraft.result)
          ? await hydrateDraft(latestDraft, items, completion)
          : null;
      const draft = restoredDraft?.full;
      if (viewRevision.current !== revision) return;
      const savedInput = conversationDrafts.current.get(
        conversation.id || latest.conversation_id,
      );
      selected.current = latest.id;
      loadedResult.current = completion?.id || null;
      setLatestCompletedID(completion?.id || "");
      setContext(
        completion
          ? structuredClone(
              completion.id === draft?.id
                ? draft.result
                : completion.result || {},
            )
          : null,
      );
      setConversationID(conversation.id || latest.conversation_id || "");
      setConversationOwner(owner);
      setForceFork(mustFork);
      setTurns(items.map(turnSummary));
      setTruncated(!!conversation.truncated);
      setJob(latest);
      setPrompt(savedInput?.prompt || "");
      setSourceURL(savedInput?.sourceURL || "");
      setSourceOpen(savedInput?.sourceOpen || false);
      setReferences(savedInput?.references || latest.references || []);
      setAttachments(
        savedInput?.jobID === latest.id
          ? savedInput.attachments || latest.attachments || []
          : latest.attachments || [],
      );
      setUploading(false);
      setEditScope(savedInput?.editScope || "auto");
      setDraftReferences([]);
      setPreview(null);
      setResult(null);
      setDraftJobID("");
      setAdopted(false);
      setMaterialized(null);
      setMaterialIssue("");
      if (draft) setSuccessfulDraft(draft, restoredDraft.issue);
      if (
        savedInput &&
        savedInput.latestCompletedID === completion?.id &&
        savedInput.draftJobID === draft?.id &&
        !draft?.materialized
      ) {
        setResult(savedInput.result);
        setContext(savedInput.context);
        setDraftReferences(savedInput.draftReferences);
      }
      setBindingId(
        savedInput?.bindingId ??
          (readTarget(draft?.id || latest.id, workspaceId)?.bindingId || ""),
      );
      setAgentID(savedInput?.agentID || latest.agent?.id || "writer");
      const preferredProvider =
        savedInput?.providerID ??
        (latest.provider_selection === "agent"
          ? ""
          : latest.provider?.id || "");
      if (
        preferredProvider === "" ||
        !providersRef.current.length ||
        providersRef.current.some((item) => item.id === preferredProvider)
      )
        setProviderID(preferredProvider);
      setEditor(null);
      setHistoryOpen(false);
      if (!mustFork) remember(workspaceId, latest);
      else if ([id, latest.id].includes(recalled(workspaceId)))
        forget(workspaceId);
    } catch (e) {
      if (viewRevision.current === revision) {
        selected.current = job?.id || null;
        setError(e.message);
      }
    } finally {
      if (viewRevision.current === revision) setBusy("");
    }
  };

  useEffect(() => {
    let stopped = false;
    api("/generation/providers")
      .then(({ items = [], default_provider_id = "" }) => {
        if (stopped) return;
        providersRef.current = items;
        setProviders(items);
        setDefaultProviderID(default_provider_id);
        setProviderError("");
        setProviderID((current) =>
          items.some((item) => item.id === current) ? current : "",
        );
      })
      .catch((e) => !stopped && setProviderError(e.message));
    return () => {
      stopped = true;
    };
  }, [model]);

  useEffect(() => {
    let stopped = false;
    let timer;
    const poll = async () => {
      try {
        await refreshConversations();
      } catch (e) {
        if (!stopped) setHistoryError(e.message);
      }
      if (!stopped && workspaceId) timer = setTimeout(poll, 2500);
    };
    poll();
    return () => {
      stopped = true;
      clearTimeout(timer);
      historyRevision.current++;
    };
  }, [workspaceId, historyScope]);

  useEffect(() => {
    if (!workspaceId) return;
    let stopped = false;
    const canRestore = () =>
      !stopped &&
      !restored.current &&
      !selected.current &&
      historyScopeRef.current === "current";
    const restoreWorkspace = async () => {
      const saved = recalled(workspaceId);
      let recovered = null;
      if (saved) {
        try {
          const previous = await api(`/generation/jobs/${saved}`);
          if (!canRestore()) return;
          if (previous.workspace_id === workspaceId) recovered = previous;
          else forget(workspaceId);
        } catch {
          if (!canRestore()) return;
          forget(workspaceId);
        }
      }
      if (!recovered && canRestore()) {
        const jobs = await api("/generation/jobs");
        if (!canRestore()) return;
        const running = jobs.items?.find(
          (item) =>
            (active(item) || item.status === "waiting_user") &&
            item.workspace_id === workspaceId,
        );
        if (running) recovered = await api(`/generation/jobs/${running.id}`);
      }
      if (!canRestore()) return;
      restored.current = true;
      if (recovered)
        await loadJob(recovered.id, { restore: true, initial: recovered });
    };
    restoreWorkspace().catch((e) => canRestore() && setHistoryError(e.message));
    return () => {
      stopped = true;
    };
  }, [workspaceId]);

  useEffect(
    () => () => {
      viewRevision.current++;
      historyRevision.current++;
    },
    [],
  );

  const jobID = pending || waiting ? job.id : null;
  const loadingConversation = busy === "load";
  useEffect(() => {
    if (!jobID || loadingConversation) return;
    let stopped = false;
    let timer;
    const poll = async () => {
      try {
        const next = await api(`/generation/jobs/${jobID}?progress=1`);
        if (stopped || selected.current !== jobID) return;
        setJob(next);
        setError("");
        if (next.status === "answered" && next.question?.answered_job_id) {
          await loadJob(next.question.answered_job_id);
          return;
        }
        if (active(next) || next.status === "waiting_user")
          timer = setTimeout(poll, active(next) ? 500 : 2000);
        else {
          refreshConversations().catch(
            (e) => !stopped && setHistoryError(e.message),
          );
        }
      } catch (e) {
        if (!stopped && selected.current === jobID) {
          setError(`任务进度读取失败：${e.message}`);
          timer = setTimeout(poll, 4000);
        }
      }
    };
    timer = setTimeout(poll, 150);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [jobID, loadingConversation]);

  useEffect(() => {
    if (job?.status !== "completed" || loadedResult.current === job.id) return;
    loadedResult.current = job.id;
    setLatestCompletedID(job.id);
    setContext(structuredClone(job.result || {}));
    if (hasDraftResult(job.result)) setSuccessfulDraft(job);
  }, [job, workspaceId]);

  useEffect(() => {
    if (
      !job ||
      ![
        "completed",
        "failed",
        "cancelled",
        "waiting_user",
        "answered",
      ].includes(job.status) ||
      refreshedMaterials.current.has(job.id)
    )
      return;
    if (
      !job.events?.some((event) =>
        ["material_saved", "material_deleted"].includes(event.stage),
      )
    )
      return;
    refreshedMaterials.current.add(job.id);
    setReferences((previous) =>
      previous.flatMap((ref) => {
        const events = job.events.filter(
          (event) =>
            event.material?.kind === ref.kind && event.material?.id === ref.id,
        );
        const latest = events.at(-1);
        if (latest?.stage === "material_deleted") return [];
        return latest?.stage === "material_saved" &&
          latest.material.version >= ref.version
          ? [{ ...ref, ...latest.material }]
          : [ref];
      }),
    );
    Promise.resolve(onMaterialsChanged?.()).catch((error) =>
      setError(`素材已保存，但列表刷新失败：${error.message}`),
    );
  }, [job, onMaterialsChanged]);

  useEffect(() => {
    if (!previewSite) {
      setPreview(null);
      return;
    }
    let stopped = false;
    setPreview(null);
    api("/sites/preview", "POST", { site: previewSite })
      .then((value) => !stopped && setPreview(value.html))
      .catch((e) => !stopped && setError(`外观预览失败：${e.message}`));
    return () => {
      stopped = true;
    };
  }, [previewSite]);

  useEffect(() => {
    followOutput.current = true;
    if (timelineRef.current)
      timelineRef.current.scrollTop = timelineRef.current.scrollHeight;
  }, [job?.id]);
  useEffect(() => {
    if (followOutput.current && timelineRef.current)
      timelineRef.current.scrollTop = timelineRef.current.scrollHeight;
  }, [
    job?.events?.length,
    job?.status,
    job?.live_response?.elapsed_ms,
    preview,
  ]);

  const run = async (action, fn) => {
    const revision = viewRevision.current;
    setBusy(action);
    setError("");
    try {
      await fn();
    } catch (e) {
      if (viewRevision.current === revision) setError(e.message);
    } finally {
      if (viewRevision.current === revision) setBusy("");
    }
  };
  const showDraft = (id) =>
    run("draft", async () => {
      const revision = viewRevision.current;
      const full = await api(`/generation/jobs/${id}`);
      if (!hasDraftResult(full.result)) return;
      const restoredDraft = await hydrateDraft(full, timeline, {
        id: latestCompletedID,
        result: context,
      });
      if (viewRevision.current !== revision) return;
      setSuccessfulDraft(restoredDraft.full, restoredDraft.issue);
      setBindingId(readTarget(id, workspaceId)?.bindingId || "");
    });
  const newConversation = () => {
    if (uploading) {
      setError("附件正在上传，请完成或取消后新建对话。");
      return;
    }
    if (!workspaceId) {
      setError("请先保存工作区，再开始对话。");
      return;
    }
    stashConversation();
    viewRevision.current++;
    selected.current = null;
    loadedResult.current = null;
    restored.current = true;
    setJob(null);
    setTurns([]);
    setTruncated(false);
    setConversationID("");
    setConversationOwner("");
    setForceFork(false);
    setDraftJobID("");
    setLatestCompletedID("");
    setContext(null);
    setResult(null);
    setPreview(null);
    setAdopted(false);
    setMaterialized(null);
    setMaterialIssue("");
    setEditor(null);
    setBusy("");
    setPrompt("");
    setReferences([]);
    setAttachments([]);
    setUploading(false);
    setEditScope("auto");
    setDraftReferences([]);
    setSourceURL("");
    setSourceOpen(false);
    setBindingId("");
    setError("");
    setHistoryOpen(false);
    setProviderID("");
    if (historyScopeRef.current !== "current") changeHistoryScope("current");
    forget(workspaceId);
    input.current?.focus();
  };
  useEffect(() => {
    if (requestedJobId) loadJob(requestedJobId);
  }, [requestedJobId]);

  const conversationGroups = Array.from(
    new Set([
      ...agents.map((a) => a.id),
      ...conversations.map((group) => group.id),
    ]),
  ).map((id) => ({
    total: 0,
    running: 0,
    ...conversations.find((group) => group.id === id),
    id,
    name:
      agents.find((a) => a.id === id)?.name ||
      conversations.find((group) => group.id === id)?.name ||
      "蜜罐编写 Agent",
  }));
  const groupedConversations = conversationGroups.filter(
    (group) => !agentFilter || agentFilter === group.id,
  );
  const start = (mode) =>
    run("start", async () => {
      if (!workspaceId) throw new Error("请先保存工作区，再开始对话。");
      if (uploading) throw new Error("请等待附件上传完成");
      if (pending) throw new Error("此对话仍在执行，可以新建对话并行处理。");
      if (waiting && currentWorkspaceJob)
        throw new Error("请先在上方问题卡片中回答 Agent，或取消等待。");
      const revision = viewRevision.current;
      restored.current = true;
      const next = await api("/generation/jobs", "POST", {
        mode,
        prompt: prompt.trim(),
        ...(!reviewing
          ? { attachments: attachments.map(({ id }) => ({ id })) }
          : {}),
        edit_scope: editScope,
        agent_id: agentID,
        // Resolve versions on the server for each new turn. A
        // material may change after selection, even if our catalogue is fresh.
        references: references.map(({ kind, id }) => ({
          kind,
          id,
        })),
        ...(sourceURL.trim() ? { source_url: sourceURL.trim() } : {}),
        workspace_id: workspaceId,
        ...(providerID ? { provider_id: providerID } : {}),
        ...(conversationID && currentWorkspaceJob
          ? { conversation_id: conversationID }
          : {}),
        ...(continuing ? { parent_job_id: continuationID } : {}),
        ...(context?.site || site ? { site: context?.site || site } : {}),
        ...(context?.scenario || scenario
          ? { scenario: context?.scenario || scenario }
          : {}),
      });
      saveTarget(next.id, workspaceId, targetBinding?.id || "");
      if (viewRevision.current !== revision) return;
      selected.current = next.id;
      setTurns((previous) =>
        historicalJob
          ? []
          : job
            ? [
                ...previous.filter((turn) => turn.id !== job.id),
                turnSummary(job),
              ]
            : previous,
      );
      if (historicalJob) {
        loadedResult.current = null;
        setTruncated(false);
        setDraftJobID("");
        setLatestCompletedID("");
        setContext(null);
        setDraftReferences([]);
        setResult(null);
        setPreview(null);
        setAdopted(false);
        setMaterialized(null);
        setMaterialIssue("");
      }
      setJob(next);
      setReferences(next.references || []);
      setAttachments(next.attachments || []);
      setConversationOwner(next.workspace_id || workspaceId);
      setForceFork(false);
      setConversationID(
        next.conversation_id || (currentWorkspaceJob ? conversationID : ""),
      );
      setPrompt("");
      setEditScope("auto");
      setSourceURL("");
      setSourceOpen(false);
      remember(workspaceId, next);
      if (historyScopeRef.current !== "current") changeHistoryScope("current");
      else refreshConversations().catch((e) => setHistoryError(e.message));
    });

  const resultContent = hasDraft && (
    <div className="agent-result">
      {changedSite && (
        <div className="composer-agent-preview">
          <div className="composer-section-line">
            <strong>
              <Eye size={16} />
              页面预览 · {result.site.name}
            </strong>
            <span className="muted">
              {result.site.files?.length || 0} 个文件
            </span>
          </div>
          {preview !== null ? (
            <iframe title="助手结果外观预览" sandbox="" srcDoc={preview} />
          ) : (
            <p className="muted">正在准备外观预览…</p>
          )}
        </div>
      )}
      {changedScenario && (
        <p className="muted">
          模拟场景：{result.scenario.name} ·{" "}
          {result.scenario.rules?.length || 0} 条响应规则
        </p>
      )}
      {draftProfile && (
        <p className="agent-result-profile">
          采用结果时绑定提示词：
          <strong>{draftProfile.name || draftProfile.id}</strong>
          {!changedScenario &&
            !targetBinding &&
            "。当前尚无场景实例，采用后仍需添加场景并绑定。"}
        </p>
      )}
      <div className="actions agent-result-actions">
        {changedSite && (
          <Button
            icon={Code2}
            disabled={!!materialized || adopted || !!busy || pending}
            onClick={() => setEditor("site")}
          >
            编辑生成的站点
          </Button>
        )}
        {changedScenario && (
          <Button
            icon={Code2}
            disabled={!!materialized || adopted || !!busy || pending}
            onClick={() => setEditor("scenario")}
          >
            编辑生成的场景
          </Button>
        )}
        <Button
          icon={MessageSquare}
          disabled={!!busy || pending}
          onClick={() => {
            setContext((current) => ({
              ...current,
              ...(changedSite ? { site: result.site } : {}),
              ...(changedScenario ? { scenario: result.scenario } : {}),
            }));
            setPrompt("");
            input.current?.focus();
          }}
        >
          继续调整
        </Button>
        <Button
          icon={Check}
          variant="primary"
          disabled={
            adopted ||
            !!materialIssue ||
            !!busy ||
            pending ||
            (!changedSite && !changedScenario)
          }
          loading={busy === "adopt"}
          onClick={() =>
            run("adopt", async () => {
              if (materialIssue) throw new Error(materialIssue);
              const material = await api(
                `/generation/jobs/${draftJobID}/materialize`,
                "POST",
                {
                  ...(changedSite ? { site: result.site } : {}),
                  ...(changedScenario ? { scenario: result.scenario } : {}),
                },
              );
              setMaterialized({
                ...(material.site ? { site_id: material.site.id } : {}),
                ...(material.scenario
                  ? { scenario_id: material.scenario.id }
                  : {}),
              });
              await onAdopt(material, targetBinding?.id);
              setAdopted(true);
              saveTarget(
                draftJobID,
                workspaceId,
                targetBinding?.id || "",
                true,
              );
            })
          }
        >
          {adopted ? "已采用到当前草稿" : "采用到当前草稿"}
        </Button>
      </div>
      <small className="muted">
        {materialIssue ||
          (adopted
            ? "素材已采用，请保存工作区。继续对话可以生成新版本。"
            : `${changedSite ? "预览保留页面外观。" : ""}采用后保存工作区并预演接口；已发布端口会随保存更新。`)}
      </small>
      {!!result.warnings?.length && (
        <details className="agent-result-notes">
          <summary>采集与生成说明 · {result.warnings.length}</summary>
          <ul>
            {result.warnings.map((warning, index) => (
              <li key={index}>{textOf(warning)}</li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );

  return (
    <Panel className="composer-generation composer-agent agent-chat-layout">
      <aside className={`agent-chat-sidebar ${historyOpen ? "is-open" : ""}`}>
        <Button
          icon={Plus}
          variant="primary"
          disabled={!workspaceId || !!busy}
          onClick={newConversation}
        >
          新建对话
        </Button>
        <Select
          aria-label="对话范围"
          className="agent-history-scope"
          value={historyScope}
          disabled={!workspaceId || !!busy}
          onChange={(event) => changeHistoryScope(event.target.value)}
        >
          <option value="current">当前工作区</option>
          <option value="unassigned">未归属历史</option>
        </Select>
        <Select
          aria-label="按 Agent 筛选对话"
          value={agentFilter}
          onChange={(e) => setAgentFilter(e.target.value)}
        >
          <option value="">全部 Agent（{conversationCount}）</option>
          {conversationGroups.map((group) => (
            <option key={group.id} value={group.id}>
              {group.name}（{group.total}）
            </option>
          ))}
        </Select>
        <button
          className="agent-history-toggle"
          aria-expanded={historyOpen}
          onClick={() => setHistoryOpen((value) => !value)}
        >
          <History size={16} />
          会话记录
        </button>
        {runningCount > 0 && (
          <p className="agent-running-hint" role="status">
            <LoaderCircle size={13} className="spin" />
            {runningCount} 个对话运行中 · 可新建或切换
          </p>
        )}
        <div className="agent-conversations" aria-label="最近会话">
          <span
            className="agent-sidebar-caption"
            title={workspaceName || undefined}
          >
            {historyScope === "current"
              ? `当前工作区${workspaceName ? ` · ${workspaceName}` : ""}`
              : "未归属历史"}
          </span>
          {historyError && (
            <p className="muted">读取会话失败：{historyError}</p>
          )}
          <span className="agent-history-total">
            共 {conversationCount} 个对话
          </span>
          {groupedConversations.map((group) => (
            <ConversationGroup
              key={`${workspaceId}:${historyScope}:${group.id}`}
              group={group}
              workspaceID={
                historyScope === "unassigned" ? "unassigned" : workspaceId
              }
              selectedID={conversationID}
              busy={busy}
              onSelect={loadJob}
              onRenamed={refreshConversations}
              statusNames={statusNames}
            />
          ))}
        </div>
      </aside>
      <div className="agent-chat-main">
        <header className="agent-chat-header">
          <h2>
            <span className="agent-avatar">
              <Bot size={20} />
            </span>
            {selectedAgent?.name || "工作区助手"}
          </h2>
          <Button icon={Settings2} variant="ghost" onClick={onReviewSettings}>
            触发设置
          </Button>
          <Button icon={Settings2} variant="ghost" onClick={onSettings}>
            模型设置
          </Button>
        </header>
        <div
          className="agent-chat-timeline"
          aria-label="对话内容"
          ref={timelineRef}
          onScroll={(event) => {
            const node = event.currentTarget;
            followOutput.current =
              node.scrollHeight - node.scrollTop - node.clientHeight < 80;
          }}
        >
          {truncated && (
            <p className="muted agent-truncated">仅展示最近 100 轮对话。</p>
          )}
          {!timeline.length && (
            <div className="agent-chat-empty">
              <span className="agent-avatar">
                <Bot size={26} />
              </span>
              <h3>描述需求，或直接提问</h3>
              <p>
                {redteaming
                  ? "从当前蜜罐首页探索接口，在隔离副本中验证提示词交付、模拟动作和回传，并生成访问画板。"
                  : reviewing
                    ? "可以询问检查方式、核查登录和接口链路，也可以追问已有报告中的异常。"
                    : "可以讨论实现思路，也可以按需生成页面或模拟场景。生成的草稿可以预览、编辑，再采用到工作区。"}
              </p>
              {!reviewing && (
                <Button icon={Globe} onClick={() => setSourceOpen(true)}>
                  从参考网址开始
                </Button>
              )}
            </div>
          )}
          {timeline.map((turn) => (
            <section
              key={turn.id}
              className="agent-chat-turn"
              data-result-kind={turn.result?.kind || "draft"}
              aria-label={turn.id === job?.id ? "助手任务" : "历史消息"}
            >
              <div className="agent-user-message">
                <p>{turn.prompt || "克隆参考页面，保留页面外观。"}</p>
                <ReferenceChips
                  references={turn.references || []}
                  label="消息引用"
                />
                <AttachmentChips files={turn.attachments || []} />
                {turn.source_url && <small>{turn.source_url}</small>}
                {turn.edit_scope?.label && (
                  <small>修改范围：{turn.edit_scope.label}</small>
                )}
                <time>{formatTime(turn.created_at)}</time>
              </div>
              <div className="agent-assistant-message">
                <span className="agent-avatar">
                  <Bot size={19} />
                </span>
                <div className="agent-message-body">
                  <div className="agent-message-summary">
                    {!(
                      turn.status === "completed" &&
                      turn.result?.kind === "message"
                    ) && (
                      <strong role={turn.id === job?.id ? "status" : undefined}>
                        {statusNames[turn.status] || "任务处理中"}
                      </strong>
                    )}
                  </div>
                  <small className="muted">
                    {turn.agent?.name || "蜜罐编写 Agent"}
                    {turn.trigger && !["manual", "chat"].includes(turn.trigger)
                      ? " · 自动触发"
                      : ""}
                  </small>
                  {turn.provider?.model && (
                    <small className="muted">{turn.provider.model}</small>
                  )}
                  {resumeDetail(turn, turns) && (
                    <p className="agent-compose-hint" role="status">
                      {resumeDetail(turn, turns)}
                    </p>
                  )}
                  <AgentActivity key={turn.id} turn={turn} />
                  {turn.question && (
                    <AgentQuestion
                      key={turn.question.id}
                      turn={turn}
                      canAnswer={turn.id === job?.id && currentWorkspaceJob}
                      disabled={!!busy}
                      onContinue={(next) => {
                        if (selected.current === turn.id)
                          return loadJob(next.id, { initial: next });
                      }}
                      onCancel={(next) => {
                        setJob(next);
                        refreshConversations().catch(() => {});
                      }}
                    />
                  )}
                  {!turn.question && (turn.summary || turn.result?.summary) && (
                    <div className="agent-answer-bubble">
                      <Suspense
                        fallback={
                          <div className="agent-answer-plain">
                            {textOf(turn.summary || turn.result.summary)}
                          </div>
                        }
                      >
                        <ChatMarkdown
                          text={textOf(turn.summary || turn.result.summary)}
                        />
                      </Suspense>
                    </div>
                  )}
                  {turn.result?.kind === "review" && (
                    <ReviewReport jobId={turn.id} report={turn.result} />
                  )}
                  {turn.error && (
                    <p className="composer-error" role="alert">
                      {textOf(turn.error)}
                    </p>
                  )}
                  {turn.id === draftJobID
                    ? resultContent
                    : isDraftTurn(turn) && (
                        <Button
                          icon={Eye}
                          disabled={!!busy || pending}
                          onClick={() => showDraft(turn.id)}
                        >
                          查看草稿
                        </Button>
                      )}
                </div>
              </div>
            </section>
          ))}
          {error && (
            <p className="composer-error" role="alert">
              {error}
            </p>
          )}
        </div>
        <div className="agent-chat-compose">
          {historicalJob && (
            <p className="agent-compose-hint agent-history-note" role="status">
              未归属历史对话，发送后将在当前工作区开启新对话。
            </p>
          )}
          {waiting && currentWorkspaceJob && (
            <p className="agent-compose-hint" role="status">
              Agent
              正在等待你的回答，请填写上方问题卡片。等待期间不消耗任务时限。
            </p>
          )}
          <AgentAttachments
            key={`attachments:${conversationID || "new"}`}
            enabled={!reviewing}
            files={attachments}
            onChange={setAttachments}
            onBusyChange={setUploading}
            disabled={
              !workspaceId ||
              !!busy ||
              pending ||
              (waiting && currentWorkspaceJob)
            }
          >
            <MentionInput
              hint={
                redteaming
                  ? selectedAgent?.redteam_mode === "controlled_replay"
                    ? "受控复现：按聊天要求验证执行与回传链路，不能判断 Agent 是否自主接受注入。"
                    : `自主行为测试：发送后按 Agent 管理中配置的独立任务启动新一轮，评测聊天不会传给被测模型。当前任务：${selectedAgent?.redteam_task || "从首页开始进行 Web 安全检查"}`
                  : reviewing
                    ? "核查已保存的工作区；可以提问、检查指定接口或追问报告。"
                    : undefined
              }
              key={conversationID || "new-conversation"}
              inputRef={input}
              value={prompt}
              onChange={setPrompt}
              references={references}
              onReferencesChange={setReferences}
              sites={sites}
              scenarios={scenarios}
              profiles={profiles}
              disabled={!workspaceId || busy === "load" || busy === "start"}
              placeholder={
                redteaming
                  ? selectedAgent?.redteam_mode === "controlled_replay"
                    ? "描述要受控复现的访问和操作步骤…"
                    : "开始测试，观察 Agent 的自主判断与实际行为…"
                  : reviewing
                    ? "描述要核查的问题，或继续追问上轮报告…"
                    : latestCompletedID
                      ? "继续描述需求或直接提问，输入 @ 引用素材…"
                      : "描述需求或直接提问，输入 @ 引用站点、场景或提示词…"
              }
              onSubmit={() => {
                if (
                  workspaceId &&
                  canGenerate &&
                  prompt.trim() &&
                  !pending &&
                  !uploading &&
                  !busy
                )
                  start("agent");
              }}
            />
          </AgentAttachments>
          {sourceOpen && !reviewing && (
            <div className="agent-source-options">
              <Field
                label="参考网址（可选）"
                hint="动态页面自动使用浏览器渲染；仅克隆页面无需模型。"
              >
                <input
                  type="url"
                  value={sourceURL}
                  onChange={(event) => setSourceURL(event.target.value)}
                  placeholder="https://example.com"
                />
              </Field>
              <Button
                icon={Globe}
                disabled={
                  !workspaceId ||
                  !sourceURL.trim() ||
                  pending ||
                  !!busy ||
                  (waiting && currentWorkspaceJob)
                }
                onClick={() => start("clone")}
              >
                仅克隆网页
              </Button>
            </div>
          )}
          <div className="agent-compose-controls">
            {!reviewing && (
              <>
                <Select
                  aria-label="本轮修改范围"
                  title="自动按本轮明确要求确定范围；未指定模块时按 @ 引用确定。只修改场景时，其他模块仅供读取。"
                  className="agent-scope-select"
                  value={editScope}
                  disabled={pending || !!busy}
                  onChange={(event) => setEditScope(event.target.value)}
                >
                  <option value="auto">修改范围 · 自动</option>
                  <option value="scenario">仅修改模拟场景</option>
                  <option value="site">仅修改站点素材</option>
                  <option value="profile">仅修改提示词</option>
                  <option value="bindings">仅修改提示词绑定</option>
                  <option value="callback">仅修改回传设置</option>
                  <option value="ports">仅修改发布与端口</option>
                  <option value="all">多个模块 · 按需求修改</option>
                  <option value="read_only">仅问答 · 不修改素材</option>
                </Select>
                <Button
                  icon={Globe}
                  variant="ghost"
                  aria-expanded={sourceOpen}
                  onClick={() => setSourceOpen((value) => !value)}
                >
                  参考网址
                </Button>
                <Select
                  aria-label="场景结果用于"
                  title="仅在生成模拟场景时选择采用位置，不会要求生成场景"
                  className="agent-scenario-select"
                  value={targetBinding ? bindingId : ""}
                  disabled={pending || !!busy}
                  onChange={(event) => {
                    setBindingId(event.target.value);
                    if (draftJobID || job?.id)
                      saveTarget(
                        draftJobID || job.id,
                        workspaceId,
                        event.target.value,
                        adopted,
                      );
                  }}
                >
                  <option value="">新增模拟场景</option>
                  {bindings.map((binding, index) => (
                    <option key={binding.id} value={binding.id}>
                      调整实例 {index + 1} ·{" "}
                      {scenarios.find((item) => item.id === binding.scenario_id)
                        ?.name || "未命名场景"}
                    </option>
                  ))}
                </Select>
              </>
            )}
            <Select
              aria-label="对话 Agent"
              value={agentID}
              disabled={pending || uploading || !!busy}
              onChange={(event) => {
                const id = event.target.value;
                if (conversationID && id !== agentID) newConversation();
                setAgentID(id);
                setProviderID("");
              }}
            >
              {agents.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </Select>
            <Select
              aria-label="本轮使用的模型"
              className="agent-provider-select"
              value={
                providers.some((item) => item.id === providerID)
                  ? providerID
                  : ""
              }
              disabled={pending || !!busy}
              onChange={(event) => setProviderID(event.target.value)}
            >
              <option value="">
                跟随 Agent ·{" "}
                {providers.find((p) => p.id === inheritedProviderID)?.model ||
                  model.model ||
                  "尚未配置模型"}
              </option>
              {providers.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name} · {item.model}
                </option>
              ))}
            </Select>
            <AgentTodo
              key={conversationID || "new-conversation"}
              plan={latestPlan}
              disabled={busy === "load"}
            />
            {pending ? (
              <Button
                icon={Square}
                loading={busy === "cancel"}
                onClick={() =>
                  run("cancel", async () => {
                    const revision = viewRevision.current;
                    const cancelled = await api(
                      `/generation/jobs/${job.id}/cancel`,
                      "POST",
                      {},
                    );
                    if (viewRevision.current !== revision) return;
                    setJob(cancelled);
                    refreshConversations().catch(() => {});
                  })
                }
              >
                取消任务
              </Button>
            ) : (
              <Button
                icon={ArrowUp}
                variant="primary"
                className="agent-send"
                aria-label="让 AI 执行"
                title="发送 · Ctrl / ⌘ + Enter"
                disabled={
                  !workspaceId ||
                  !prompt.trim() ||
                  !canGenerate ||
                  uploading ||
                  !!busy ||
                  (waiting && currentWorkspaceJob)
                }
                loading={busy === "start"}
                onClick={() => start("agent")}
              >
                发送
              </Button>
            )}
          </div>
          {!workspaceId && (
            <small className="agent-compose-hint" role="status">
              请先保存工作区，再开始对话或克隆网页。
            </small>
          )}
          {(providerError || modelError || !canGenerate) && (
            <small className="agent-compose-hint">
              {providerError ||
                modelError ||
                "配置模型后可使用 AI，也可直接克隆参考网页。"}
            </small>
          )}
        </div>
      </div>
      {editor === "site" &&
        result?.site &&
        renderSiteEditor({
          initial: result.site,
          draftOnly: true,
          onClose: () => setEditor(null),
          onSave: async (value) => {
            setResult((current) => ({ ...current, site: value }));
            setContext((current) => ({ ...current, site: value }));
            setEditor(null);
          },
        })}
      {editor === "scenario" &&
        result?.scenario &&
        renderScenarioEditor({
          initial: result.scenario,
          draftOnly: true,
          onClose: () => setEditor(null),
          onSave: async (value) => {
            setResult((current) => ({ ...current, scenario: value }));
            setContext((current) => ({ ...current, scenario: value }));
            setEditor(null);
          },
        })}
    </Panel>
  );
}
