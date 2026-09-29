package lab

import (
	"bytes"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func accessProbe(b *testLab, listener Doc, ip, ua, method, path, run string, body Doc) *httptest.ResponseRecorder {
	b.t.Helper()
	var content []byte
	if body != nil {
		content = jsonBytes(body)
	}
	req := httptest.NewRequest(method, "http://honeypot.invalid"+path, bytes.NewReader(content))
	req.RemoteAddr = net.JoinHostPort(ip, "45200")
	req.Header.Set("User-Agent", ua)
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	if run != "" {
		req.Header.Set("X-Run-ID", run)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	b.app.handler(false, str(listener["id"]), integer(listener["port"])).ServeHTTP(res, req)
	return res
}

func TestAccessFlowCorrelatesActualIPAcrossPortsAndSessions(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	first, second := b.listener(deployment), b.listener(deployment)
	ip := "192.0.2.10"
	entry := accessProbe(b, first, ip, "Agent A", "GET", "/", "", nil)
	run := entry.Header().Get("X-Run-ID")
	if run == "" || entry.Code != 200 {
		t.Fatal("entry failed")
	}
	for _, path := range []string{"/missing", "/api/download?file=wrong.txt", "/api/download?file=notes.txt"} {
		accessProbe(b, first, ip, "Agent A", "GET", path, run, nil)
	}
	accessProbe(b, first, ip, "Agent A", "HEAD", "/api/download?file=notes.txt", run, nil)
	bad := accessProbe(b, first, ip, "Agent A", "POST", "/collect", "", Doc{"run_id": run, "token": "wrong", "data": Doc{"synthetic": true}})
	if bad.Code != 403 {
		t.Fatal("invalid receipt was accepted")
	}
	accepted := accessProbe(b, first, "192.0.2.20", "Callback client", "POST", "/collect", "", Doc{"run_id": run, "token": issueReceiptToken(b, run), "data": Doc{"synthetic": true}})
	if accepted.Code != 201 {
		t.Fatalf("valid receipt failed: %s", accepted.Body.String())
	}
	accessProbe(b, second, ip, "Agent B", "GET", "/", "", nil)
	list := b.api("/session-sources", "GET", nil, 200)
	if integer(list["total"]) != 2 {
		t.Fatalf("spoofed IP or missing sources: %v", list)
	}
	var source Doc
	for _, raw := range array(list["items"]) {
		if object(raw)["ip"] == ip {
			source = object(raw)
		}
	}
	if integer(source["session_count"]) != 2 || integer(source["listener_count"]) != 2 || integer(source["delivery_count"]) != 1 || integer(source["report_count"]) != 0 || integer(source["user_agent_count"]) != 2 {
		t.Fatalf("incorrect cross-port correlation, HEAD delivery or callback attribution: %v", source)
	}
	flow := b.api("/session-sources/flow?ip="+ip, "GET", nil, 200)
	graph := object(flow["access_flow"])
	if integer(flow["total"]) != 7 || len(array(graph["groups"])) != 2 {
		t.Fatalf("missing traffic: %v", flow)
	}
	ids, statuses := map[string]bool{}, map[int]bool{}
	deliveries, callbacks := 0, 0
	for _, raw := range array(graph["nodes"]) {
		node := object(raw)
		ids[str(node["id"])] = true
		evidence := object(node["evidence"])
		if evidence["ip"] != ip {
			t.Fatal("different source leaked into board")
		}
		if node["kind"] == "request" {
			statuses[integer(evidence["status"])] = true
		}
		if node["kind"] == "delivery" {
			deliveries++
		}
		if node["kind"] == "callback" {
			callbacks++
			if node["status"] != "fail" {
				t.Fatal("rejected callback shown as accepted")
			}
		}
	}
	if deliveries != 1 || callbacks != 1 || !statuses[404] || !statuses[403] {
		t.Fatal("missing failed probes or incorrect delivery evidence")
	}
	for _, raw := range array(graph["edges"]) {
		e := object(raw)
		if !ids[str(e["source"])] || !ids[str(e["target"])] || e["relation"] == "next" {
			t.Fatal("invalid or fabricated navigation edge")
		}
	}
	filtered := b.api("/session-sources/flow?ip="+ip+"&ua="+url.QueryEscape("Agent B"), "GET", nil, 200)
	if integer(filtered["total"]) != 1 || len(array(filtered["user_agents"])) != 2 {
		t.Fatal("UA filter lost alternatives")
	}
	b.api("/session-sources/flow?ip=bad", "GET", nil, 400)
	b.api("/session-sources/flow?ip="+ip+"&from=2026-09-20T00:00:00Z&to=2026-09-19T00:00:00Z", "GET", nil, 400)
	b.api("/session-sources/flow?ip="+ip+"&page=-1", "GET", nil, 400)
	b.api("/session-sources/flow?ip="+ip+"&before=oops", "GET", nil, 400)
	b.restart()
	reloaded := b.api("/session-sources/flow?ip="+ip, "GET", nil, 200)
	if dump(reloaded["access_flow"]) != dump(graph) {
		t.Fatal("restart changed observations")
	}
	b.api("/sessions", "DELETE", nil, 200)
	if count(b.app.store.db, "SELECT COUNT(*) FROM access_requests") != 0 {
		t.Fatal("clear left IP history behind")
	}
}

func TestAccessFlowRecordsHostedDownloadsAndFailures(t *testing.T) {
	b := newLab(t)
	_, _, _, workspace, content := linkedSitesFixture(b)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	listener := b.listener(deployment)
	res := accessProbe(b, listener, "192.0.2.30", "Download agent", "GET", "/oss/vpn/downloads/client.zip", "", nil)
	if !bytes.Equal(res.Body.Bytes(), content) || res.Code != 200 {
		t.Fatal("observer altered download")
	}
	accessProbe(b, listener, "192.0.2.30", "Download agent", "HEAD", "/oss/vpn/downloads/client.zip", res.Header().Get("X-Run-ID"), nil)
	flow := b.api("/session-sources/flow?ip=192.0.2.30", "GET", nil, 200)
	downloads := 0
	for _, raw := range array(object(flow["access_flow"])["nodes"]) {
		n := object(raw)
		if n["kind"] == "download" {
			downloads++
			if object(n["evidence"])["site_node_id"] != "vpn" {
				t.Fatal("subsite lost")
			}
		}
	}
	if downloads != 1 {
		t.Fatal("missing download or counted HEAD as download")
	}
}

func TestAccessFlowLegacyImportAndStablePagination(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	dep := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	l := b.listener(dep)
	res := accessProbe(b, l, "192.0.2.40", "Legacy agent", "GET", "/api/download?file=notes.txt", "", nil)
	if res.Code != 200 {
		t.Fatal("fixture failed")
	}
	// Simulate the schema upgrade of an installation with old session events.
	exec(b.app.store.db, "DELETE FROM access_requests")
	exec(b.app.store.db, "DELETE FROM access_log_meta")
	b.app.store.initAccessLog()
	oldCount := count(b.app.store.db, "SELECT COUNT(*) FROM access_requests")
	b.app.store.initAccessLog()
	if oldCount < 3 || count(b.app.store.db, "SELECT COUNT(*) FROM access_requests") != oldCount {
		t.Fatal("legacy import missing or duplicated")
	}
	for _, row := range rows(b.app.store.db, "SELECT detail FROM access_requests") {
		if decode(str(row["detail"]))["source"] != "legacy_session" {
			t.Fatal("legacy evidence advertised as captured traffic")
		}
	}
	for i := 0; i < 105; i++ {
		accessProbe(b, l, "192.0.2.50", "Paging agent", "GET", fmt.Sprintf("/step/%d", i), "", nil)
	}
	first := b.api("/session-sources/flow?ip=192.0.2.50", "GET", nil, 200)
	accessProbe(b, l, "192.0.2.50", "Paging agent", "GET", "/newest", "", nil)
	second := b.api(fmt.Sprintf("/session-sources/flow?ip=192.0.2.50&page=2&before=%d", integer(first["snapshot_id"])), "GET", nil, 200)
	if integer(first["total"]) != 105 || integer(second["total"]) != 105 {
		t.Fatal("pagination snapshot changed with live traffic")
	}
	seen := map[string]bool{}
	for _, page := range []Doc{first, second} {
		for _, raw := range array(object(page["access_flow"])["nodes"]) {
			n := object(raw)
			if seen[str(n["id"])] || strings.Contains(str(n["label"]), "newest") {
				t.Fatal("pagination duplicated or skipped historical requests")
			}
			seen[str(n["id"])] = true
		}
	}
	if len(seen) != 105 {
		t.Fatal("not all requests reachable across pages")
	}
}
