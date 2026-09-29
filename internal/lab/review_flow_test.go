package lab

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestReviewFlowCompletionKeepsAllPublishedGroups(t *testing.T) {
	for _, role := range []string{"reviewer", "redteam"} {
		for _, interrupted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/interrupted=%t", role, interrupted), func(t *testing.T) {
				b := newLab(t)
				_, _, workspace := composerFixture(b)
				m := b.app.generationManager()
				id := randomHex(12)
				r := &workspaceReviewer{manager: m, jobID: id, snapshot: reviewSnapshot(b.app.store.db, str(workspace["id"])), config: Doc{"role": role}}
				if role == "redteam" {
					r.initRedteam()
				}
				m.saveJob(Doc{"id": id, "mode": "review", "status": "running", "workspace_id": workspace["id"], "fingerprint": snapshotFingerprint(r.snapshot)})
				endpoint := "/workspace-reviews/" + str(workspace["id"]) + "/flow?job=" + id
				addGroup := func(kind string, count int) {
					group := r.flowGroup(kind, kind)
					previous := ""
					for i := 0; i < count; i++ {
						node := r.flowRequest(group, previous, Doc{"method": "GET", "path": fmt.Sprintf("/step/%d", i), "status": 200}, "observed", "")
						previous = node
					}
				}
				addGroup("http", 44)
				r.publishFlow()
				initial := b.api(endpoint, "GET", nil, 200)
				if len(array(object(initial["access_flow"])["nodes"])) != 44 {
					t.Fatal("live HTTP flow missing")
				}
				addGroup("browser", 10)
				r.addCheck("访问完成", "pass", "保留全部访问证据", nil)
				r.publishFlow()
				live := b.api(endpoint, "GET", nil, 200)
				if len(array(object(live["access_flow"])["nodes"])) != 54 {
					t.Fatal("late browser group replaced previous flow")
				}
				var ctxErr error
				if interrupted {
					ctxErr = context.Canceled
					job := m.job(id)
					job["status"] = "cancelled"
					m.saveJob(job)
				}
				m.finishReview(r, "", ctxErr)
				completed := b.api(endpoint, "GET", nil, 200)
				if completed["status"] == "running" || dump(completed["access_flow"]) != dump(live["access_flow"]) {
					t.Fatal("finalization changed the published flow")
				}
				if m.job(id)["access_flow"] != nil {
					t.Fatal("test did not exercise switching to the persisted result")
				}
				b.restart()
				reloaded := b.api(endpoint, "GET", nil, 200)
				if dump(reloaded["access_flow"]) != dump(live["access_flow"]) {
					t.Fatal("restart lost historical flow")
				}
			})
		}
	}
}

func TestReviewFlowPersistsObservedDeliveryAndKeepsChecksIndependent(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	job := b.api("/workspace-reviews/"+str(workspace["id"]), "POST", Doc{}, 202)
	done := generationWait(t, b.app, str(job["id"]))
	flow := object(object(done["result"])["access_flow"])
	if flow == nil || len(array(flow["nodes"])) == 0 {
		t.Fatal("no graph recorded")
	}
	ids := map[string]bool{}
	deliveries := 0
	for _, raw := range array(flow["nodes"]) {
		n := object(raw)
		ids[str(n["id"])] = true
		if n["kind"] == "delivery" {
			deliveries++
			if str(object(n["evidence"])["profile_id"]) == "" {
				t.Fatal("delivery lacks source")
			}
		}
	}
	if deliveries == 0 {
		t.Fatal("real delivery was not recorded")
	}
	for _, raw := range array(flow["edges"]) {
		e := object(raw)
		if !ids[str(e["source"])] || !ids[str(e["target"])] {
			t.Fatal("dangling edge")
		}
		if e["relation"] == "next" {
			t.Fatal("synthetic checks fabricated a continuous path")
		}
	}
	endpoint := "/workspace-reviews/" + str(workspace["id"]) + "/flow?job=" + str(job["id"])
	live := b.api(endpoint, "GET", nil, 200)
	if dump(live["access_flow"]) != dump(flow) || boolean(live["stale"]) {
		t.Fatal("board snapshot mismatch")
	}
	// Editing a bound material, without changing workspace version, invalidates
	// freshness without replacing the immutable review evidence.
	site := get(b.app.store.db, "sites", str(workspace["site_id"]))
	site["name"] = "Updated material"
	b.api("/sites", "POST", site, 200)
	stale := b.api(endpoint, "GET", nil, 200)
	if !boolean(stale["stale"]) || dump(stale["access_flow"]) != dump(flow) {
		t.Fatal("historical flow overwritten or stale material missed")
	}
	other := b.api("/workspaces", "POST", Doc{"name": "Other", "slug": "other-flow", "bindings": []any{}}, 200)
	b.api("/workspace-reviews/"+str(other["id"])+"/flow?job="+str(job["id"]), "GET", nil, 404)
}

func TestReviewFlowOnlyActualEventsProduceDeliveryNodes(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	r := &workspaceReviewer{snapshot: reviewSnapshot(b.app.store.db, str(workspace["id"]))}
	if err := r.open(); err != nil {
		t.Fatal(err)
	}
	defer r.sandbox.close()
	group := r.flowGroup("http", "Probe")
	var deliveryRule Doc
	for _, rule := range r.sandbox.rules {
		if boolean(rule["delivery_required"]) {
			deliveryRule = rule
			break
		}
	}
	if deliveryRule == nil {
		t.Fatal("fixture missing delivery rule")
	}
	req := reviewExample(deliveryRule)
	req["method"] = "HEAD"
	res := r.sandbox.request(req, true)
	r.flowRequest(group, "", res, "observed", "")
	// A response body merely mentioning a prompt is not delivery evidence.
	r.flowRequest(group, "", Doc{"path": "/text", "method": "GET", "body": "{{prompt}}", "prompt_required": true}, "observed", "")
	for _, raw := range r.flow.nodes {
		if object(raw)["kind"] == "delivery" {
			t.Fatal("fabricated delivery from HEAD/text")
		}
	}
	r.suite(context.Background())
	if len(r.flow.groups) != 2 {
		t.Fatal("suite merged into manual probe")
	}
}

func TestReviewFlowBoundsEvidenceAndNeverCreatesDanglingEdges(t *testing.T) {
	r := &workspaceReviewer{snapshot: Doc{"workspace": Doc{"version": 1}}}
	group := r.flowGroup("http", "Large")
	for i := 0; i < 800; i++ {
		r.flowRequest(group, "", Doc{"path": "/large", "method": "GET", "body": strings.Repeat("x", 9000), "observed_rules": []any{Doc{"rule_id": "r"}}, "observed_deliveries": []any{Doc{"rule_id": "r", "profile_name": "p"}}}, "observed", "")
	}
	if len(r.flow.nodes) != maxReviewFlowNodes || !r.flow.truncated {
		t.Fatal("flow cap missing")
	}
	ids := map[string]bool{}
	for _, raw := range r.flow.nodes {
		n := object(raw)
		ids[str(n["id"])] = true
		if len(str(object(n["evidence"])["body_excerpt"])) > 2100 {
			t.Fatal("unbounded response")
		}
	}
	for _, raw := range r.flow.edges {
		e := object(raw)
		if !ids[str(e["source"])] || !ids[str(e["target"])] {
			t.Fatal("dangling truncated edge")
		}
	}
}
