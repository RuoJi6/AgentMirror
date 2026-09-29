package lab

import (
	"testing"
	"time"
)

func TestOverviewTrafficRangeAndTimezone(t *testing.T) {
	b := newLab(t)
	s := b.app.store
	now := time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)
	add := func(at, ip string, delivered, received int, detail Doc) {
		exec(s.db, `INSERT INTO access_requests(at,ip,detail,delivered,received) VALUES(?,?,?,?,?)`, at, ip, dump(detail), delivered, received)
	}
	add("2026-09-19T16:00:00.000+00:00", "192.0.2.1", 2, 0, Doc{"status": 200, "download": Doc{"filename": "test.zip"}})
	add("2026-09-19T15:59:59.000+00:00", "192.0.2.1", 0, 1, Doc{"status": 201})
	add("2026-09-19T17:00:00.000+00:00", "192.0.2.2", 0, 0, Doc{"status": 403})
	add("2026-09-19T18:00:00.000+00:00", "", 0, 0, Doc{"kind": "file_download", "source": "legacy_session"})
	add("2026-09-12T12:00:00.000+00:00", "192.0.2.3", 1, 1, Doc{"status": 500})
	add("2026-09-20T16:00:00.000+00:00", "192.0.2.4", 1, 1, Doc{"status": 302})
	result := s.overview(now, 7, 480)
	traffic := object(result["traffic"])
	if integer(traffic["requests"]) != 4 || integer(traffic["deliveries"]) != 1 || integer(traffic["reports"]) != 1 || integer(traffic["downloads"]) != 2 || integer(traffic["sources"]) != 2 {
		t.Fatal(traffic)
	}
	daily := array(result["daily"])
	if len(daily) != 7 || object(daily[6])["date"] != "2026-09-20" || integer(object(daily[6])["requests"]) != 3 || integer(object(daily[5])["requests"]) != 1 || integer(object(daily[0])["requests"]) != 0 {
		t.Fatal(daily)
	}
	statuses := object(result["statuses"])
	if integer(statuses["success"]) != 2 || integer(statuses["client_error"]) != 1 || integer(statuses["unknown"]) != 1 || integer(statuses["server_error"]) != 0 {
		t.Fatal(statuses)
	}
	if _, exists := result["tokens"]; exists {
		t.Fatal("unexpected model accounting")
	}
	b.api("/overview?days=2", "GET", nil, 400)
	b.api("/overview?offset=99999", "GET", nil, 400)
	b.api("/overview?days=7", "GET", nil, 200)
	b.api("/overview?days=90&offset=-480", "GET", nil, 200)
}

func TestOverviewHostedDownloadIsSeparateFromReceipt(t *testing.T) {
	b := newLab(t)
	_, _, _, workspace, _ := linkedSitesFixture(b)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	listener := b.listener(deployment)
	res := accessProbe(b, listener, "192.0.2.30", "Download agent", "GET", "/oss/vpn/downloads/client.zip", "", nil)
	if res.Code != 200 {
		t.Fatal(res.Code)
	}
	accessProbe(b, listener, "192.0.2.30", "Download agent", "HEAD", "/oss/vpn/downloads/client.zip", res.Header().Get("X-Run-ID"), nil)
	accessProbe(b, listener, "192.0.2.30", "Download agent", "GET", "/missing-file.zip", res.Header().Get("X-Run-ID"), nil)
	result := b.app.store.overview(time.Now(), 30, 480)
	traffic, summary := object(result["traffic"]), object(result["summary"])
	if integer(traffic["downloads"]) != 1 || integer(summary["downloads"]) != 1 || integer(summary["reports"]) != 0 || integer(traffic["reports"]) != 0 {
		t.Fatal(result)
	}
	if integer(object(result["workspaces"])["published"]) != 1 || len(array(result["recent"])) != 1 {
		t.Fatal(result)
	}
}
