package lab

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFreeResultsAndBoundedReadback(t *testing.T) {
	b := newLab(t)
	p := get(b.app.store.db, "profiles", "receipt")
	p["body"] = "{{run_id}} {{token}} {{callback_url}} {{result.system_time}} {{result.output}}"
	p = b.api("/profiles", "POST", p, 200)
	preview := b.api("/profiles/receipt/preview", "POST", p, 200)
	if !strings.Contains(str(preview["instruction"]), "PREVIEW_RUN") || !strings.Contains(str(preview["instruction"]), "{{result.output}}") {
		t.Fatal("result placeholder evaluated or system variable lost")
	}
	l := b.listener(b.deployment())
	session, _ := b.visit(l)
	id := str(session["id"])
	base := str(l["public_url"])
	token := object(session["snapshot"])["token"]
	var last string
	for _, value := range []any{"text", nil, true, 123, []any{1, "two"}, Doc{"arbitrary": true}, strings.Repeat("<img src=x onerror=alert(1)>\n", 6000)} {
		r := b.request(base, "/collect", "POST", Doc{"run_id": id, "token": token, "data": value})
		expectStatus(t, r, 201)
		last = fmtPort(integer(r.doc()["receipt_id"]))
	}
	compact := b.api("/sessions/"+id+"?reports_page=1", "GET", nil, 200)
	if integer(compact["report_count"]) != 7 || len(array(compact["reports"])) != 5 {
		t.Fatal("not paginated", compact)
	}
	for _, r := range array(compact["reports"]) {
		item := object(r)
		if _, ok := item["body"]; ok {
			t.Fatal("full body loaded into list")
		}
		if len([]rune(str(item["preview"]))) > 600 {
			t.Fatal("unbounded preview")
		}
	}
	full := b.api("/sessions/"+id+"/reports/"+last, "GET", nil, 200)
	if len(str(full["content"])) < 100000 || !strings.Contains(str(full["content"]), "onerror") {
		t.Fatal("content lost or sanitized before storage")
	}
	second, _ := b.visit(l)
	b.api("/sessions/"+str(second["id"])+"/reports/"+last, "GET", nil, 404)
	b.api("/sessions/"+id+"?reports_page=0", "GET", nil, 400)
	compact = b.api("/sessions/"+id+"?reports_page=2", "GET", nil, 200)
	if len(array(compact["reports"])) != 2 {
		t.Fatal("second page lost")
	}
	b.app.store.write(func(q queryer) {
		for i := 0; i < 120; i++ {
			event(q, id, "request", Doc{"path": "/synthetic"})
		}
	})
	compact = b.api("/sessions/"+id+"?reports_page=1", "GET", nil, 200)
	if len(array(compact["events"])) != 100 || integer(compact["event_count"]) < 120 {
		t.Fatal("unbounded events")
	}
	// Deep but valid JSON is retrieved as text, not a recursively rendered tree.
	deep := `{"run_id":"` + id + `","token":"` + str(token) + `","data":` + strings.Repeat("[", 2000) + `0` + strings.Repeat("]", 2000) + `}`
	req, _ := http.NewRequest("POST", base+"/collect", strings.NewReader(deep))
	req.Header.Set("Content-Type", "application/json")
	res, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatal(res.StatusCode)
	}
}

func TestTextReceiverAuthenticationAndSize(t *testing.T) {
	b := newLab(t)
	l := b.listener(b.deployment())
	s, _ := b.visit(l)
	id, token := str(s["id"]), str(object(s["snapshot"])["token"])
	post := func(text, run, secret string) int {
		req, _ := http.NewRequest("POST", str(l["public_url"])+"/collect", strings.NewReader(text))
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		req.Header.Set("X-Run-ID", run)
		req.Header.Set("X-Run-Token", secret)
		r, err := b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		io.Copy(io.Discard, r.Body)
		return r.StatusCode
	}
	if post("<script>alert(1)</script>", id, token) != 201 {
		t.Fatal("text not accepted")
	}
	if post("test", id, "wrong") != 403 || post("test", "", "") != 400 {
		t.Fatal("auth bypass")
	}
	if post(strings.Repeat("x", maxBody+1), id, token) != 413 {
		t.Fatal("text size limit missing")
	}
	expectStatus(t, b.request(str(l["public_url"]), "/collect", "POST", Doc{"run_id": id, "token": token, "data": strings.Repeat("x", maxBody)}), 413)
	req, _ := http.NewRequest("POST", str(l["public_url"])+"/collect", bytes.NewReader(jsonBytes(Doc{"run_id": id, "token": token, "data": "x"})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Run-ID", "mismatch")
	r, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatal("conflicting envelope accepted")
	}
}
