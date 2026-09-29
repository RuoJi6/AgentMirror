package lab

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

func reviewPolicy(q queryer, id string) Doc {
	d := Doc{"id": id, "version": 0, "agent_id": "reviewer", "after_save": false, "after_writer": false, "after_failure": false, "on_tool": false, "interval_seconds": 0, "tool_names": []any{}, "messages": Doc{}, "browser_steps": []any{}}
	if old := entityMaybe(q, "review_policies", id); old != nil {
		merge(d, old)
	}
	return d
}

// All authored entities are captured under one transaction. Runs never read a
// mixture of old rules and newly edited profiles, or write production sessions.
func reviewSnapshot(q queryer, id string) Doc {
	w := get(q, "workspaces", id)
	snap := Doc{"workspace": w, "sites": []any{}, "scenarios": []any{}, "profiles": []any{}, "collect_response": collectResponseConfig(settings(q))}
	snap["hosted_files"] = []any{}
	seenSites, seenFiles := map[string]bool{}, map[string]bool{}
	for _, node := range workspaceSiteNodes(w) {
		id := str(node["site_id"])
		if id == "" || seenSites[id] {
			continue
		}
		site := get(q, "sites", id)
		seenSites[id] = true
		snap["sites"] = append(array(snap["sites"]), site)
		for _, raw := range hostedFileMetadata(q, site) {
			f := object(raw)
			if !seenFiles[str(f["id"])] {
				snap["hosted_files"] = append(array(snap["hosted_files"]), f)
				seenFiles[str(f["id"])] = true
			}
		}
	}
	seen := map[string]bool{}
	for _, value := range array(w["bindings"]) {
		b := object(value)
		sid := str(b["scenario_id"])
		if !seen["s:"+sid] {
			snap["scenarios"] = append(array(snap["scenarios"]), get(q, "scenarios", sid))
			seen["s:"+sid] = true
		}
		ids := []string{str(b["profile_id"])}
		for _, v := range object(b["rule_profiles"]) {
			ids = append(ids, str(v))
		}
		sort.Strings(ids)
		for _, pid := range ids {
			if pid != "" && !seen["p:"+pid] {
				snap["profiles"] = append(array(snap["profiles"]), get(q, "profiles", pid))
				seen["p:"+pid] = true
			}
		}
	}
	return snap
}

func snapshotFingerprint(snap Doc) string { return fmt.Sprintf("%x", sha256.Sum256(jsonBytes(snap))) }

func (m *generationManager) reviewRoute(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/workspace-reviews/"), "/")
	if len(parts) < 1 || len(parts) > 2 || !idPattern.MatchString(parts[0]) {
		fail(404, "核查接口不存在")
	}
	id := parts[0]
	get(m.store.db, "workspaces", id)
	if len(parts) == 2 && parts[1] == "flow" && r.Method == "GET" {
		jobID := textField(Doc{"id": r.URL.Query().Get("job")}, "id", 64, true)
		job := m.job(jobID)
		if job["workspace_id"] != id || job["mode"] != "review" {
			fail(404, "此工作区不存在该核查记录")
		}
		flow := object(object(job["result"])["access_flow"])
		if flow == nil {
			flow = object(job["access_flow"])
		}
		var fingerprint string
		m.store.write(func(q queryer) { fingerprint = snapshotFingerprint(reviewSnapshot(q, id)) })
		response(w, r, Doc{"id": jobID, "status": job["status"], "agent": job["agent"], "created_at": job["created_at"], "access_flow": flow, "stale": fingerprint != str(job["fingerprint"]), "workspace_version": job["workspace_version"]}, 200, "", nil)
		return true
	}
	if len(parts) == 2 && parts[1] == "policy" {
		if r.Method == "GET" {
			p := reviewPolicy(m.store.db, id)
			p["trigger_error"] = entityMaybe(m.store.db, "review_cursors", id)["last_error"]
			response(w, r, p, 200, "", nil)
			return true
		}
		if r.Method == "POST" {
			raw := generationBody(w, r)
			config := m.resolveAgent(raw, "reviewer")
			var saved Doc
			m.store.write(func(q queryer) {
				old := reviewPolicy(q, id)
				if integer(old["version"]) != integer(raw["version"]) {
					fail(409, "核查设置已更新，请刷新")
				}
				saved = validateReviewPolicy(raw, old, config)
				put(q, "review_policies", saved)
				// Enabling a trigger starts watching future changes, not old jobs.
				put(q, "review_cursors", Doc{"id": id, "fingerprint": snapshotFingerprint(reviewSnapshot(q, id)), "writer_after": timestamp(), "failure_after": timestamp(), "tool_after": timestamp(), "last_run": timestamp()})
			})
			response(w, r, saved, 200, "", nil)
			return true
		}
	}
	if len(parts) == 1 && r.Method == "GET" {
		items := []any{}
		for _, row := range rows(m.store.db, "SELECT doc FROM generation_jobs WHERE json_extract(doc,'$.mode')='review' AND json_extract(doc,'$.workspace_id')=? ORDER BY json_extract(doc,'$.created_at') DESC LIMIT 30", id) {
			d := decode(str(row["doc"]))
			delete(d, "events")
			delete(d, "conversation")
			delete(d, "access_flow")
			if result := object(d["result"]); result != nil {
				d["result"] = Doc{"verdict": result["verdict"], "counts": result["counts"]}
			}
			items = append(items, d)
		}
		response(w, r, Doc{"items": items}, 200, "", nil)
		return true
	}
	if len(parts) == 1 && r.Method == "POST" {
		raw := generationBody(w, r)
		response(w, r, m.startReview(id, "manual", raw), 202, "", nil)
		return true
	}
	fail(405, "请求方法不支持")
	return true
}

func (m *generationManager) startReview(workspaceID, trigger string, raw Doc) Doc {
	policy := reviewPolicy(m.store.db, workspaceID)
	raw = clone(raw)
	if str(raw["agent_id"]) == "" {
		raw["agent_id"] = policy["agent_id"]
	}
	role := "reviewer"
	if get(m.store.db, "agent_definitions", str(raw["agent_id"]))["role"] == "redteam" {
		role = "redteam"
	}
	config := m.resolveAgent(raw, role)
	prompt := textField(raw, "prompt", 12000, trigger == "chat")
	if prompt == "" {
		prompt = reviewTriggerMessage(policy, trigger)
	}
	var snapshot Doc
	m.store.write(func(q queryer) { snapshot = reviewSnapshot(q, workspaceID) })
	settings := Doc{}
	provider := textField(raw, "provider_id", 64, false)
	if provider == "" {
		provider = str(config["provider_id"])
	}
	if provider == "" {
		provider = generationDefaultProvider(m.store.db)
	}
	if provider != "" {
		settings = generationProvider(m.store.db, provider, true)
	}
	if (trigger == "chat" || role == "redteam") && len(settings) == 0 {
		fail(400, "请先为核查 Agent 配置模型")
	}
	if len(settings) > 0 && role != "redteam" {
		settings["mcp_snapshots"] = m.store.mcpSnapshots(config)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if answered := m.existingQuestionAnswer(raw); answered != nil {
		return answered
	}
	if m.closed {
		fail(503, "核查服务正在关闭")
	}
	for id := range m.active {
		job := m.job(id)
		if trigger != "chat" && job["mode"] == "review" && job["workspace_id"] == workspaceID {
			fail(409, "此工作区正在核查，自动触发将在完成后继续")
		}
	}
	if len(m.active) >= maxConcurrentGenerationJobs {
		fail(429, "AI 任务数已达上限")
	}
	id := randomHex(12)
	input := Doc{"workspace_id": workspaceID, "agent": config, "parent_job_id": textField(raw, "parent_job_id", 64, false)}
	conversationID := m.jobConversationID(raw, input, id)
	conversation := []any{}
	previous := Doc{}
	// Shared conversation persistence is used by manual and triggered reviews.
	// Only bounded conversational text and compact prior evidence are replayed.
	items, roots := m.conversationIndex()
	var inheritedReferences any
	for _, item := range items {
		if roots[str(item["id"])] != conversationID {
			continue
		}
		old := m.job(str(item["id"]))
		if str(old["prompt"]) != "" {
			conversation = append(conversation, Doc{"role": "user", "content": str(old["prompt"])})
		}
		result := object(old["result"])
		if str(result["summary"]) != "" {
			conversation = append(conversation, Doc{"role": "assistant", "content": boundedText(str(result["summary"]), 8000)})
		}
		if result["kind"] == "question" && object(result["review_progress"]) != nil {
			previous = object(result["review_progress"])
		}
		if result["kind"] == "review" {
			previous = result
		}
		inheritedReferences = old["references"]
	}
	if len(conversation) > 8 {
		conversation = conversation[len(conversation)-8:]
	}
	conversation = append(conversation, Doc{"role": "user", "content": prompt})
	references, _ := m.resolveReferences(raw, inheritedReferences)
	now := timestamp()
	job := Doc{"id": id, "mode": "review", "status": "running", "workspace_id": workspaceID, "workspace_version": object(snapshot["workspace"])["version"], "fingerprint": snapshotFingerprint(snapshot), "agent": config, "trigger": trigger, "created_at": now, "updated_at": now, "events": []any{}, "conversation": conversation, "conversation_id": conversationID, "prompt": prompt, "references": references, "parent_job_id": input["parent_job_id"]}
	if len(settings) > 0 {
		job["provider_id"] = settings["id"]
		job["provider"] = generationProviderAudit(settings)
	}
	job["provider_selection"] = "agent"
	if str(raw["provider_id"]) != "" {
		job["provider_selection"] = "override"
	}
	m.saveStartedJob(job, raw)
	ctx, cancel := context.WithTimeout(context.Background(), generationTimeout(settings, "job_timeout_seconds"))
	m.active[id] = cancel
	m.wg.Add(1)
	go m.executeReview(ctx, id, snapshot, policy, config, settings, trigger, previous)
	return clone(job)
}

func (m *generationManager) finishReview(r *workspaceReviewer, message string, ctxErr error) {
	defer func() { _ = recover() }()
	if r.question != nil && ctxErr == nil {
		result := questionResult(r.question)
		result["review_progress"] = r.result()
		m.finish(r.jobID, result, "", nil)
		return
	}
	if message != "" || ctxErr != nil {
		detail := message
		if ctxErr != nil {
			detail = ctxErr.Error()
		}
		r.addCheck("核查中断", "incomplete", detail+"；已完成的检查保留，其余链路尚未确认", nil)
	}
	result := r.result()
	m.finish(r.jobID, result, message, ctxErr)
	// Cancellation marks the job before the worker exits. Preserve the evidence
	// even when the generic generation finalizer keeps that terminal status.
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.job(r.jobID)
	d["result"] = result
	delete(d, "access_flow")
	m.saveJob(d)
}

func (m *generationManager) executeReview(ctx context.Context, id string, snapshot, policy, config, settings Doc, trigger string, previous Doc) {
	r := &workspaceReviewer{manager: m, jobID: id, snapshot: snapshot, config: config, checks: []any{}, notes: []any{}, previous: previous}
	if config["role"] == "redteam" {
		r.initRedteam()
	}
	defer m.wg.Done()
	defer func() {
		if p := recover(); p != nil {
			m.finishReview(r, caught(p).Error(), ctx.Err())
		}
		m.mu.Lock()
		if cancel := m.active[id]; cancel != nil {
			cancel()
		}
		delete(m.active, id)
		m.mu.Unlock()
	}()
	defer func() {
		if r.sandbox != nil {
			r.sandbox.close()
		}
	}()
	if trigger != "chat" && r.redteam == nil {
		if err := r.open(); err != nil {
			r.addCheck("工作区配置", "fail", err.Error(), nil)
			m.finishReview(r, "", ctx.Err())
			return
		}
		if toolBound(config, "run_review_suite") {
			r.call(ctx, "run_review_suite", Doc{})
		} else {
			r.addCheck("接口覆盖", "incomplete", "Agent 未绑定 run_review_suite，未执行接口逐条核查", nil)
		}
		steps := array(policy["browser_steps"])
		if len(steps) > 0 && toolBound(config, "run_browser_flow") {
			r.call(ctx, "run_browser_flow", Doc{"steps": steps})
		}
	}
	if len(settings) > 0 {
		if err := r.runModel(ctx, settings); err != nil {
			r.addCheck("Agent 核查", "incomplete", err.Error(), nil)
		}
	} else {
		r.addCheck("Agent 核查", "incomplete", "尚未配置模型；已完成可执行的接口检查，无法自主规划页面操作", nil)
	}
	if r.question != nil {
		m.finishReview(r, "", ctx.Err())
		return
	}
	if r.redteam != nil {
		m.finishReview(r, "", ctx.Err())
		return
	}
	if len(r.checks) == 0 && trigger == "chat" {
		m.finishReview(r, "", ctx.Err())
		return
	}
	if trigger == "chat" && !r.suiteRan {
		r.addCheck("核查范围", "incomplete", "本轮仅执行对话指定的局部检查，未运行完整接口核查", nil)
		m.finishReview(r, "", ctx.Err())
		return
	}
	if r.browserRan && r.browserAssertions == 0 {
		r.addCheck("页面内容断言", "incomplete", "浏览器未完成内容或元素可见性断言，不能确认登录后信息展示正确", nil)
	}
	if r.sandbox != nil {
		for _, rule := range r.sandbox.rules {
			if !r.browserCovered[promptBindingKey(rule)] {
				r.addCheck("页面可达性 · "+str(rule["method"])+" "+str(rule["path"]), "incomplete", "接口已独立检查，但浏览器流程尚未触发此规则；请补充登录后操作或由 Agent 继续核查", Doc{"binding_id": rule["binding_id"], "rule_id": rule["id"]})
			}
		}
	}
	if !r.browserRan {
		r.addCheck("页面访问链路", "incomplete", "未执行浏览器流程，登录与登录后入口尚未确认；配置浏览器步骤或绑定模型后重新核查", nil)
	}
	m.finishReview(r, "", ctx.Err())
}

func (m *generationManager) startReviewScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	m.schedulerCancel = cancel
	m.schedulerWG.Add(1)
	go func() {
		defer m.schedulerWG.Done()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.pollReviewTriggers()
			}
		}
	}()
}
