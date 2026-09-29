package lab

import (
	"os"
	"testing"
	"time"
)

// This optional check exercises the real configured model with synthetic input.
// Credentials are read from an external private file and never logged.
func TestAgentLiveIntent(t *testing.T) {
	filename := os.Getenv("AGENTMIRROR_LIVE_CONFIG")
	if filename == "" {
		t.Skip("AGENTMIRROR_LIVE_CONFIG is not set")
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal("cannot read private provider config")
	}
	settings, err := parseAgentJSON(string(raw))
	if err != nil {
		t.Fatal("invalid private provider config")
	}
	a := generationTestApp(t)
	generationRequest(t, a, "POST", "settings", settings, 200)
	before := count(a.store.db, "SELECT COUNT(*) FROM entities")
	run := func(input Doc) Doc {
		t.Helper()
		job := generationRequest(t, a, "POST", "jobs", input, 202)
		deadline := time.Now().Add(185 * time.Second)
		for time.Now().Before(deadline) {
			current := a.generation.job(str(job["id"]))
			if current["status"] == "completed" {
				return current
			}
			if current["status"] == "failed" || current["status"] == "cancelled" {
				t.Fatalf("live intent request failed: %s", str(current["error"]))
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("live intent request timed out")
		return nil
	}
	capability := run(Doc{"prompt": "你可以做什么？"})
	reply := object(capability["result"])
	if reply["kind"] != "message" || str(reply["summary"]) == "" || len(array(reply["changes"])) != 0 || reply["site"] != nil || reply["scenario"] != nil {
		t.Fatal("capability question must answer without generating materials")
	}
	for _, event := range array(capability["events"]) {
		if object(event)["stage"] == "validation" {
			t.Fatal("capability reply was forced through draft repair")
		}
	}
	t.Log("capability question returned a message with no generated material")

	onlyScenario := run(Doc{"prompt": "只创建一个模拟场景：GET /api/info 返回 text/plain，正文为演示信息和 {{prompt}} 提示词槽位。不需要页面。", "parent_job_id": capability["id"]})
	scenarioResult := object(onlyScenario["result"])
	if scenarioResult["kind"] != "draft" || scenarioResult["site"] != nil || object(scenarioResult["scenario"]) == nil || len(array(scenarioResult["changes"])) != 1 || !has(array(scenarioResult["changes"]), "scenario") {
		t.Fatal("scenario request must create only the requested scenario")
	}
	t.Log("scenario-only request produced a scenario without a site")

	question := run(Doc{"prompt": "解释一下刚才这个场景会在什么时候返回提示词？", "parent_job_id": onlyScenario["id"]})
	questionResult := object(question["result"])
	if questionResult["kind"] != "message" || len(array(questionResult["changes"])) != 0 || str(questionResult["summary"]) == "" || dump(questionResult["scenario"]) != dump(scenarioResult["scenario"]) {
		t.Fatal("follow-up question must answer without changing the existing scenario")
	}
	t.Log("follow-up explanation retained the unchanged scenario and conversation")

	onlySite := run(Doc{"prompt": "只创建一个最小的 index.html，页面标题为“测试门户”，正文是一个欢迎标题。不需要接口或模拟场景。"})
	siteResult := object(onlySite["result"])
	if siteResult["kind"] != "draft" || object(siteResult["site"]) == nil || siteResult["scenario"] != nil || len(array(siteResult["changes"])) != 1 || !has(array(siteResult["changes"]), "site") {
		t.Fatal("site request must create only the requested site")
	}
	if count(a.store.db, "SELECT COUNT(*) FROM entities") != before {
		t.Fatal("live conversation automatically saved or published materials")
	}
	t.Log("site-only request produced a site without a scenario; no materials were adopted or published")
}
