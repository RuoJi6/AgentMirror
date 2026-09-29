package lab

func agentQuestionTool() Doc {
	return generationTool("ask_user", "Pause and ask the user ONE concise question when required information is missing or a consequential choice is ambiguous (e.g. target site, port/address, file to upload, overwrite/delete intent). Offer optional choices; free text is always allowed. Do not ask about routine implementation details already authorized. Call alone and do not perform dependent mutations until the answer. Progress is saved; waiting consumes no model requests or task timeout.", Doc{"question": Doc{"type": "string", "description": "Self-contained question, including why the answer is needed"}, "options": Doc{"type": "array", "items": Doc{"type": "string"}, "maxItems": 5, "description": "Optional suggested answers; never preselect or assume a choice"}}, "question")
}
func validateAgentQuestion(args Doc) Doc {
	question := textField(args, "question", 2000, true)
	options := []any{}
	if args["options"] != nil && array(args["options"]) == nil {
		fail(400, "提问选项必须为数组")
	}
	if len(array(args["options"])) > 5 {
		fail(400, "提问最多提供 5 个选项")
	}
	for _, raw := range array(args["options"]) {
		value := textField(Doc{"option": raw}, "option", 300, true)
		if has(options, value) {
			fail(400, "提问选项不能重复")
		}
		options = append(options, value)
	}
	return Doc{"id": randomHex(12), "text": question, "options": options, "created_at": timestamp()}
}
func questionResult(question Doc) Doc {
	return Doc{"kind": "question", "question": question, "summary": question["text"]}
}

// The endpoint chooses the original owner/role, never values supplied by a
// browser or model. start/startReview repeat validation while holding m.mu.
func (m *generationManager) answerQuestion(id string, raw Doc) Doc {
	parent := m.job(id)
	questionID := textField(raw, "question_id", 64, true)
	answer := textField(raw, "answer", 12000, true)
	q := object(parent["question"])
	if q["id"] != questionID {
		fail(409, "问题已更新，请刷新后回答")
	}
	input := Doc{"mode": "agent", "answer_to": id, "question_id": questionID, "prompt": answer, "workspace_id": parent["workspace_id"], "agent_id": object(parent["agent"])["id"], "provider_id": parent["provider_id"], "conversation_id": parent["conversation_id"], "parent_job_id": id, "edit_scope": object(parent["edit_scope"])["mode"]}
	// Do not send stale reference versions back as optimistic-lock requirements.
	refs := []any{}
	for _, r := range array(parent["references"]) {
		d := object(r)
		refs = append(refs, Doc{"kind": d["kind"], "id": d["id"]})
	}
	input["references"] = refs
	if raw["attachments"] != nil {
		input["attachments"] = m.resolveAttachments(raw, nil)
	}
	return m.start(input)
}

// Must run under the generation start mutex; retries resolve to the same child.
func (m *generationManager) existingQuestionAnswer(raw Doc) Doc {
	id := str(raw["answer_to"])
	if id == "" {
		return nil
	}
	parent := m.job(id)
	q := object(parent["question"])
	if str(raw["workspace_id"]) != str(parent["workspace_id"]) || str(raw["agent_id"]) != str(object(parent["agent"])["id"]) || str(raw["parent_job_id"]) != id || str(raw["conversation_id"]) != str(parent["conversation_id"]) {
		fail(409, "回答必须继续原工作区、Agent 和对话")
	}
	textField(raw, "prompt", 12000, true)
	if q["id"] != raw["question_id"] {
		fail(409, "问题已更新，请刷新后回答")
	}
	if next := str(q["answered_job_id"]); next != "" {
		if q["answer"] != raw["prompt"] || dump(array(m.resolveAttachments(raw, parent["attachments"]))) != dump(m.resolveAttachments(m.job(next), nil)) {
			fail(409, "此问题已经回答，请在对话中发送新消息")
		}
		return publicGenerationJob(m.job(next))
	}
	if parent["status"] != "waiting_user" {
		fail(409, "此任务当前没有等待回答的问题")
	}
	if m.active[id] != nil {
		fail(409, "正在保存提问前的进度，请稍后提交回答")
	}
	return nil
}

// Persist the answer and continuation together. A crash/retry cannot create
// duplicate children or lose the association between a question and its answer.
func (m *generationManager) saveStartedJob(job, raw Doc) {
	if str(raw["answer_to"]) == "" {
		m.saveJob(job)
		return
	}
	parent := m.job(str(raw["answer_to"]))
	q := object(parent["question"])
	merge(q, Doc{"answer": raw["prompt"], "answered_at": timestamp(), "answered_job_id": job["id"]})
	parent["status"], parent["updated_at"] = "answered", timestamp()
	m.store.write(func(db queryer) {
		exec(db, "INSERT INTO generation_jobs(id,doc) VALUES(?,?)", job["id"], dump(job))
		exec(db, "UPDATE generation_jobs SET doc=? WHERE id=?", dump(parent), parent["id"])
	})
}

const agentQuestionInstructions = `
When missing essential user information, call ask_user with a concise question and optional choices. Examples include an unspecified port/public address, ambiguous site/file target, or a requested deletion with dependencies. The server saves progress and suspends this turn. Do not guess an answer, continue dependent work, or include other tools after ask_user in the same message. Users may always type their own answer. Writer questions also allow users to upload files directly; resumed attachments contain their uploaded metadata. A resumed answer continues the original task and scope; read fresh versions before further saves. Do not ask redundant questions when the user already provided the information.`
