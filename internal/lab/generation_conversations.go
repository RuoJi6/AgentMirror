package lab

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Select only display metadata: site files and provider conversation blocks can
// be megabytes long and must not travel with the conversation rail.
func (m *generationManager) conversationIndex() ([]Doc, map[string]string) {
	items := []Doc{}
	byID := map[string]Doc{}
	for _, row := range rows(m.store.db, `SELECT json_object(
		'id',id,'conversation_id',json_extract(doc,'$.conversation_id'),
		'parent_job_id',json_extract(doc,'$.parent_job_id'),'workspace_id',json_extract(doc,'$.workspace_id'),
		'prompt',json_extract(doc,'$.prompt'),'source_url',json_extract(doc,'$.source_url'),
		'created_at',json_extract(doc,'$.created_at'),'updated_at',json_extract(doc,'$.updated_at'),
		'provider_selection',json_extract(doc,'$.provider_selection'),'mode',json_extract(doc,'$.mode'),'trigger',json_extract(doc,'$.trigger'),'status',json_extract(doc,'$.status'),'provider',json_extract(doc,'$.provider'),
		'references',json_extract(doc,'$.references'),'attachments',json_extract(doc,'$.attachments'),
		'edit_scope',json_extract(doc,'$.edit_scope'),'question',json_extract(doc,'$.question'),
		'agent',CASE WHEN json_extract(doc,'$.agent') IS NULL THEN NULL ELSE json_object('id',json_extract(doc,'$.agent.id'),'name',json_extract(doc,'$.agent.name'),'version',json_extract(doc,'$.agent.version'),'role',json_extract(doc,'$.agent.role')) END,
		'result',CASE WHEN json_extract(doc,'$.result') IS NULL THEN NULL ELSE json_object(
			'kind',COALESCE(json_extract(doc,'$.result.kind'),'draft'),'review_type',json_extract(doc,'$.result.review_type'),
			'changes',json_extract(doc,'$.result.changes'),'counts',json_extract(doc,'$.result.counts'),'verdict',json_extract(doc,'$.result.verdict'),
			'redteam',CASE WHEN json_extract(doc,'$.result.redteam') IS NULL THEN NULL ELSE json_object('mode',json_extract(doc,'$.result.redteam.mode'),'outcome_label',json_extract(doc,'$.result.redteam.outcome_label')) END,
			'site_present',json_type(doc,'$.result.site')='object',
			'scenario_present',json_type(doc,'$.result.scenario')='object'
		) END
	) AS metadata FROM generation_jobs ORDER BY json_extract(doc,'$.created_at'),rowid`) {
		d := decode(str(row["metadata"]))
		if result := object(d["result"]); result != nil {
			if result["changes"] == nil {
				changes := []any{}
				if result["kind"] != "message" {
					for _, key := range []string{"site", "scenario"} {
						if integer(result[key+"_present"]) == 1 {
							changes = append(changes, key)
						}
					}
				}
				result["changes"] = changes
			}
			delete(result, "site_present")
			delete(result, "scenario_present")
		}
		items = append(items, d)
		byID[str(d["id"])] = d
	}
	roots := map[string]string{}
	var resolve func(string, map[string]bool) string
	resolve = func(id string, seen map[string]bool) string {
		if root := roots[id]; root != "" {
			return root
		}
		d := byID[id]
		root := str(d["conversation_id"])
		if root == "" {
			root = id
			parent := str(d["parent_job_id"])
			if parent != "" && byID[parent] != nil && !seen[parent] {
				seen[id] = true
				root = resolve(parent, seen)
			}
		}
		roots[id] = root
		return root
	}
	for _, d := range items {
		resolve(str(d["id"]), map[string]bool{})
	}
	return items, roots
}

func conversationTitle(d Doc) string {
	title := str(d["prompt"])
	if title == "" {
		title = str(d["source_url"])
	}
	if title == "" {
		title = "新对话"
	}
	return boundedText(title, 80)
}

func (m *generationManager) allConversations(workspaceID string) []any {
	if workspaceID != "" && !idPattern.MatchString(workspaceID) {
		fail(400, "工作区编号不正确")
	}
	items, roots := m.conversationIndex()
	groups := map[string]Doc{}
	for _, d := range items {
		id := roots[str(d["id"])]
		g := groups[id]
		if g == nil {
			// The first turn owns the conversation, including legacy unassigned
			// histories. Reading the index never migrates or reassigns old jobs.
			g = Doc{"id": id, "workspace_id": str(d["workspace_id"]), "title": conversationTitle(d), "turn_count": 0}
			groups[id] = g
		}
		g["turn_count"] = integer(g["turn_count"]) + 1
		g["latest_job_id"], g["status"], g["provider"], g["references"] = d["id"], d["status"], d["provider"], d["references"]
		g["result"] = d["result"]
		g["question"] = d["question"]
		g["agent"], g["mode"], g["trigger"] = d["agent"], d["mode"], d["trigger"]
		// A late event from an earlier turn must not move the rail backwards.
		if str(d["updated_at"]) > str(g["updated_at"]) {
			g["updated_at"] = d["updated_at"]
		}
	}
	workspaces := map[string]bool{}
	if workspaceID == "unassigned" {
		for _, row := range rows(m.store.db, "SELECT id FROM entities WHERE kind='workspaces'") {
			workspaces[str(row["id"])] = true
		}
	}
	titles := map[string]Doc{}
	for _, metadata := range listing(m.store.db, "generation_conversations") {
		titles[str(metadata["id"])] = metadata
	}
	result := []any{}
	for _, g := range groups {
		g["title_version"] = 0
		if metadata := titles[str(g["id"])]; metadata != nil {
			g["title"], g["title_version"] = metadata["title"], metadata["version"]
		}

		owner := str(g["workspace_id"])
		if workspaceID == "unassigned" {
			if workspaces[owner] {
				continue
			}
		} else if workspaceID != "" && owner != workspaceID {
			continue
		}
		result = append(result, g)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := object(result[i]), object(result[j])
		if str(a["updated_at"]) == str(b["updated_at"]) {
			return str(a["id"]) > str(b["id"])
		}
		return str(a["updated_at"]) > str(b["updated_at"])
	})
	return result
}

// Preserve the legacy API while the sidebar uses explicit pagination.
func (m *generationManager) listConversations(workspaceID string) []any {
	result := m.allConversations(workspaceID)
	if len(result) > 50 {
		result = result[:50]
	}
	return result
}

func (m *generationManager) conversation(id string) Doc {
	items, roots := m.conversationIndex()
	selected := []Doc{}
	for _, d := range items {
		if roots[str(d["id"])] == id {
			selected = append(selected, d)
		}
	}
	if len(selected) == 0 {
		fail(404, "对话不存在")
	}
	title, workspaceID, total := conversationTitle(selected[0]), str(selected[0]["workspace_id"]), len(selected)
	if len(selected) > 100 {
		selected = selected[len(selected)-100:]
	}
	turns := []any{}
	for _, d := range selected {
		var raw string
		check(m.store.db.QueryRow(`SELECT json_object(
			'summary',json_extract(doc,'$.result.summary'),'error',json_extract(doc,'$.error'),
			'events',json_extract(doc,'$.events'),'plan',json_extract(doc,'$.plan'),
			'mode',json_extract(doc,'$.mode'),'materialized',json_extract(doc,'$.materialized'),
			'resume_state',json_extract(doc,'$.resume_state')
		) FROM generation_jobs WHERE id=?`, d["id"]).Scan(&raw))
		for key, value := range decode(raw) {
			d[key] = value
		}
		// Full tool and reasoning text is fetched only when expanded.
		for _, value := range array(d["events"]) {
			event := object(value)
			if event["input"] != nil || event["output"] != nil || event["reasoning"] != nil {
				event["has_payload"] = true
				delete(event, "input")
				delete(event, "output")
				delete(event, "reasoning")
			}
		}
		d["conversation_id"] = id
		turns = append(turns, d)
	}
	version := 0
	if metadata := entityMaybe(m.store.db, "generation_conversations", id); metadata != nil {
		title, version = str(metadata["title"]), integer(metadata["version"])
	}
	return Doc{"id": id, "workspace_id": workspaceID, "title": title, "title_version": version, "turns": turns, "total_turns": total, "truncated": total > len(turns)}
}

func (m *generationManager) jobConversationID(raw, input Doc, newID string) string {
	requested := textField(raw, "conversation_id", 64, false)
	parent := str(input["parent_job_id"])
	if requested == "" && parent == "" {
		return newID
	}
	if requested != "" && !idPattern.MatchString(requested) {
		fail(400, "对话编号不正确")
	}
	items, roots := m.conversationIndex()
	validateWorkspace := func(conversationID string) {
		for _, item := range items {
			if roots[str(item["id"])] != conversationID {
				continue
			}
			if str(item["workspace_id"]) != str(input["workspace_id"]) {
				fail(409, "对话不属于当前工作区，请新建对话后继续")
			}
			expectedAgent := str(object(input["agent"])["id"])
			actualAgent := str(object(item["agent"])["id"])
			if actualAgent == "" {
				actualAgent = "writer"
			}
			if expectedAgent != "" && expectedAgent != actualAgent {
				fail(409, "此对话属于其他 Agent，请新建对话后切换角色")
			}
			// start holds m.mu through validation and insertion, so concurrent
			// submissions from different tabs cannot append overlapping turns.
			if item["status"] == "waiting_user" && item["id"] != raw["answer_to"] {
				fail(409, "此对话正在等待回答，请先提交问题卡片或取消提问")
			}
			if item["status"] == "running" || item["status"] == "queued" {
				fail(409, "此对话仍在执行，请等待完成或取消当前任务；也可以新建对话并行处理")
			}
			if item["status"] == "cancelled" && m.active[str(item["id"])] != nil {
				fail(409, "上个任务正在结束并保存进度，请稍后继续")
			}
		}
	}
	if parent != "" {
		root := roots[parent]
		if root == "" {
			fail(404, "父任务不存在")
		}
		if requested != "" && requested != root {
			fail(409, "父任务不属于当前对话")
		}
		validateWorkspace(root)
		return root
	}
	for _, root := range roots {
		if requested == root {
			validateWorkspace(requested)
			return requested
		}
	}
	fail(404, "对话不存在")
	return ""
}

// The summary poll carries counts/revisions only. Expanded groups fetch five
// rows, with workspace and Agent filtering applied before the page boundary.
func (m *generationManager) conversationPage(query url.Values) Doc {
	items := m.allConversations(query.Get("workspace_id"))
	groups := map[string]Doc{}
	grouped := map[string][]any{}
	for _, value := range items {
		item := object(value)
		agent := object(item["agent"])
		id, name := str(agent["id"]), str(agent["name"])
		if id == "" {
			id = "writer"
		}
		if name == "" {
			name = "蜜罐编写 Agent"
		}
		if groups[id] == nil {
			groups[id] = Doc{"id": id, "name": name, "total": 0, "running": 0}
		}
		group := groups[id]
		group["total"] = integer(group["total"]) + 1
		if item["status"] == "queued" || item["status"] == "running" {
			group["running"] = integer(group["running"]) + 1
		}
		grouped[id] = append(grouped[id], value)
	}
	if query.Get("summary") == "1" {
		ids := []string{}
		for id := range groups {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		result := []any{}
		for _, id := range ids {
			group := groups[id]
			group["revision"] = fmt.Sprintf("%x", sha256.Sum256([]byte(dump(grouped[id]))))
			result = append(result, group)
		}
		return Doc{"groups": result, "total": len(items)}
	}
	page := 1
	if raw := query.Get("page"); raw != "" {
		var err error
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			fail(400, "页码需为正整数")
		}
	}
	if id := query.Get("agent_id"); id != "" {
		if !idPattern.MatchString(id) {
			fail(400, "Agent 编号不正确")
		}
		items = grouped[id]
	}
	const size = 5
	total := len(items)
	pages := max(1, (total+size-1)/size)
	page = min(page, pages)
	start := (page - 1) * size
	selected := []any{}
	selected = append(selected, items[start:min(start+size, total)]...)
	return Doc{"items": selected, "page": page, "pages": pages, "page_size": size, "total": total}
}

func (m *generationManager) renameConversation(id string, raw Doc) Doc {
	if !idPattern.MatchString(id) {
		fail(404, "对话不存在")
	}
	title := textField(raw, "title", 80, true)
	if strings.ContainsAny(title, "\r\n") {
		fail(400, "对话名称不能包含换行")
	}
	// Missing/fractional versions must not silently overwrite another tab.
	expected, valid := number(raw["version"])
	if !valid || expected < 0 {
		fail(400, "缺少有效的对话名称版本，请刷新后重试")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, roots := m.conversationIndex()
	exists := false
	for _, root := range roots {
		if root == id {
			exists = true
			break
		}
	}
	if !exists {
		fail(404, "对话不存在")
	}
	var result Doc
	m.store.write(func(q queryer) {
		current := entityMaybe(q, "generation_conversations", id)
		version := integer(current["version"])
		if expected != int64(version) {
			fail(409, "对话名称已更新，请刷新后重试")
		}
		put(q, "generation_conversations", Doc{"id": id, "title": title, "version": version + 1})
		result = Doc{"id": id, "title": title, "title_version": version + 1}
	})
	return result
}
