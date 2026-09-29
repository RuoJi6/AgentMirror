package lab

import "fmt"

// The board is an evidence projection, never a model-authored assertion of
// reachability. Browser steps, HTTP probes and synthetic suite checks occupy
// separate groups. Only actual delivery events produce delivery nodes.
const maxReviewFlowNodes = 600

type reviewFlow struct {
	groups, nodes, edges []any
	truncated            bool
}

func (r *workspaceReviewer) flowGroup(kind, label string) string {
	if r.flow == nil {
		r.flow = &reviewFlow{}
	}
	id := fmt.Sprintf("flow-%d", len(r.flow.groups)+1)
	r.flow.groups = append(r.flow.groups, Doc{"id": id, "kind": kind, "label": label})
	return id
}

func (r *workspaceReviewer) flowNode(group, kind, label, status string, evidence Doc) string {
	if r.flow == nil || len(r.flow.nodes) >= maxReviewFlowNodes {
		if r.flow != nil {
			r.flow.truncated = true
		}
		return ""
	}
	id := fmt.Sprintf("node-%d", len(r.flow.nodes)+1)
	r.flow.nodes = append(r.flow.nodes, Doc{"id": id, "group": group, "kind": kind, "label": boundedText(label, 220), "status": status, "evidence": evidence})
	return id
}

func (r *workspaceReviewer) flowEdge(from, to, relation string) {
	if from != "" && to != "" {
		r.flow.edges = append(r.flow.edges, Doc{"source": from, "target": to, "relation": relation})
	}
}

func (r *workspaceReviewer) flowRequest(group, parent string, res Doc, status, checkID string) string {
	res["_flow_delivery_nodes"] = []any{}
	evidence := Doc{"method": res["method"], "path": res["path"], "status": res["status"], "body_bytes": res["body_bytes"], "download": res["download"], "body_omitted": res["body_omitted"], "check_id": checkID, "headers": res["headers"], "body_excerpt": boundedText(str(res["body"]), 2000), "body_truncated": len([]rune(str(res["body"]))) > 2000}
	id := r.flowNode(group, "request", str(res["method"])+" "+str(res["path"]), status, evidence)
	r.flowEdge(parent, id, "request")
	rules := map[string]string{}
	for _, raw := range array(res["observed_rules"]) {
		observed := clone(object(raw))
		label := "命中规则 · " + str(observed["rule_id"])
		if r.sandbox != nil {
			for _, rule := range r.sandbox.rules {
				if rule["id"] == observed["rule_id"] && rule["binding_id"] == observed["binding_id"] {
					name := str(rule["name"])
					if name == "" {
						name = str(rule["id"])
					}
					label = str(rule["scenario_name"]) + " · " + name
					observed["conditions"] = rule["conditions"]
					observed["scenario_id"] = rule["scenario_id"]
				}
			}
		}
		ruleID := r.flowNode(group, "rule", label, "observed", observed)
		r.flowEdge(id, ruleID, "matched")
		rules[str(observed["rule_id"])] = ruleID
	}
	for _, raw := range array(res["observed_deliveries"]) {
		d := clone(object(raw))
		d["meaning"] = "响应已交付此提示词；不代表访问者已执行提示词。"
		delivery := r.flowNode(group, "delivery", "提示词交付 · "+str(d["profile_name"]), "observed", d)
		res["_flow_delivery_nodes"] = append(array(res["_flow_delivery_nodes"]), delivery)
		parent := rules[str(d["rule_id"])]
		if parent == "" {
			parent = id
		}
		r.flowEdge(parent, delivery, "delivered")
	}
	return id
}

func (r *workspaceReviewer) flowResult() Doc {
	if r.flow == nil {
		return nil
	}
	return Doc{"version": 1, "groups": r.flow.groups, "nodes": r.flow.nodes, "edges": r.flow.edges, "truncated": r.flow.truncated, "environment": "isolated_saved_workspace", "workspace_version": object(r.snapshot["workspace"])["version"], "fingerprint": snapshotFingerprint(r.snapshot)}
}

func (r *workspaceReviewer) publishFlow() {
	if r.flow == nil || r.manager == nil || r.jobID == "" {
		return
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	job := r.manager.job(r.jobID)
	job["access_flow"] = r.flowResult()
	r.manager.saveJob(job)
}
