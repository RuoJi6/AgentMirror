package lab

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAdminInitializationProtectsManagement(t *testing.T) {
	b := newUnauthedLab(t)
	status := b.api("/auth/status", "GET", nil, 200)
	if status["initialized"] != false || status["authenticated"] != false {
		t.Fatal(status)
	}
	b.csrf = str(status["csrf"])
	for _, p := range []string{"/state", "/sessions", "/export", "/preview/login", "/profiles/receipt/versions", "/auth/password", "/auth/unknown"} {
		b.api(p, "GET", nil, 401)
	}
	b.api("/profiles", "POST", Doc{"name": "unauthenticated"}, 401)
	expectStatus(t, b.request(b.admin, "/setup", "GET", nil), 200)
	b.api("/auth/setup", "POST", Doc{"username": "admin", "password": "short", "confirm_password": "short"}, 400)
	b.api("/auth/setup", "POST", Doc{"username": "admin", "password": "long-enough-password", "confirm_password": "different-password"}, 400)
	b.authenticate(true)
	b.api("/auth/setup", "POST", Doc{"username": "intruder", "password": "a-different-password", "confirm_password": "a-different-password"}, 409)
	username, encoded := b.app.store.adminCredentials()
	if username != "testadmin" || !strings.HasPrefix(encoded, "argon2id-v1$") || strings.Contains(encoded, "SYNTHETIC") {
		t.Fatal("invalid credential storage")
	}
	for _, p := range []string{"/state", "/export"} {
		body := b.request(b.admin, "/api"+p, "GET", nil).body
		if bytes.Contains(body, []byte(encoded)) || bytes.Contains(body, []byte("password_hash")) {
			t.Fatal("credential data leaked through management API")
		}
	}
	parsed, _ := url.Parse(b.admin + "/api/state")
	cookies := b.client.Jar.Cookies(parsed)
	if len(cookies) == 0 {
		t.Fatal("missing session cookie")
	}
	var digest string
	check(b.app.store.db.QueryRow("SELECT token_hash FROM admin_sessions LIMIT 1").Scan(&digest))
	if digest == cookies[0].Value || len(digest) != 64 {
		t.Fatal("raw session token was persisted")
	}
}

func TestAdminSetupConcurrentAndOriginChecks(t *testing.T) {
	b := newUnauthedLab(t)
	b.csrf = str(b.api("/auth/status", "GET", nil, 200)["csrf"])
	raw := jsonBytes(Doc{"username": "admin", "password": "parallel-test-password", "confirm_password": "parallel-test-password"})
	for _, mode := range []string{"missing nonce", "cross origin"} {
		req, _ := http.NewRequest("POST", b.admin+"/api/auth/setup", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if mode == "cross origin" {
			req.Header.Set("X-Admin-Token", b.csrf)
			req.Header.Set("Origin", "http://evil.invalid")
		}
		response, err := b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatalf("%s: %d", mode, response.StatusCode)
		}
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// No shared cookie jar: both requests represent independent initializers.
			client := &http.Client{Transport: b.client.Transport, Timeout: 5 * time.Second}
			req, _ := http.NewRequest("POST", b.admin+"/api/auth/setup", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Admin-Token", b.csrf)
			response, err := client.Do(req)
			if err != nil {
				statuses <- 0
				return
			}
			defer response.Body.Close()
			io.Copy(io.Discard, response.Body)
			statuses <- response.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 || count(b.app.store.db, "SELECT COUNT(*) FROM admin_account") != 1 {
		t.Fatal("setup is not atomic", counts)
	}
}

func TestAdminLogoutPasswordChangeAndExpiry(t *testing.T) {
	b := newLab(t)
	parsed, _ := url.Parse(b.admin + "/api/state")
	oldCookie := b.client.Jar.Cookies(parsed)[0]
	assertCookieRejected := func(cookie *http.Cookie) {
		t.Helper()
		client := &http.Client{Transport: b.client.Transport, Timeout: 5 * time.Second}
		req, _ := http.NewRequest("GET", b.admin+"/api/state", nil)
		req.AddCookie(cookie)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("revoked cookie accepted")
		}
	}
	b.api("/auth/logout", "POST", Doc{}, 200)
	b.api("/state", "GET", nil, 401)
	assertCookieRejected(oldCookie)
	b.csrf = str(b.api("/auth/status", "GET", nil, 200)["csrf"])
	b.api("/auth/login", "POST", Doc{"username": "testadmin", "password": "wrong"}, 401)
	b.authenticate(false)
	oldCookie = b.client.Jar.Cookies(parsed)[0]
	newPassword := "new-SYNTHETIC-auth-password"
	result := b.api("/auth/password", "POST", Doc{"current_password": "SYNTHETIC_AUTH_PASSWORD_2026", "new_password": newPassword, "confirm_password": newPassword}, 200)
	b.csrf = str(result["csrf"])
	b.api("/state", "GET", nil, 200)
	assertCookieRejected(oldCookie)
	if count(b.app.store.db, "SELECT COUNT(*) FROM admin_sessions") != 1 {
		t.Fatal("password change did not invalidate sessions")
	}
	b.api("/auth/logout", "POST", Doc{}, 200)
	b.csrf = str(b.api("/auth/status", "GET", nil, 200)["csrf"])
	b.api("/auth/login", "POST", Doc{"username": "testadmin", "password": "SYNTHETIC_AUTH_PASSWORD_2026"}, 401)
	login := b.request(b.admin, "/api/auth/login", "POST", Doc{"username": "testadmin", "password": newPassword})
	expectStatus(t, login, 200)
	b.csrf = str(login.doc()["csrf"])
	cookie := (&http.Response{Header: login.header}).Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api" || cookie.MaxAge != 43200 {
		t.Fatal("unsafe cookie attributes", cookie)
	}
	// Login survives a process restart without reinitializing or reauthenticating.
	b.app.Close()
	var err error
	b.app, err = New(b.opts)
	if err != nil {
		t.Fatal(err)
	}
	b.api("/state", "GET", nil, 200)
	exec(b.app.store.db, "UPDATE admin_sessions SET expires_at=?", time.Now().Add(-time.Minute).Unix())
	b.api("/state", "GET", nil, 401)
	if b.api("/auth/status", "GET", nil, 200)["authenticated"] != false {
		t.Fatal("expired login reported active")
	}
}

func TestAdminLoginRateLimitAndPublicIsolation(t *testing.T) {
	b := newLab(t)
	l := b.listener(b.deployment())
	session, _ := b.visit(l)
	for _, p := range []string{"/api/auth/status", "/api/auth/setup", "/api/auth/login", "/api/state"} {
		expectStatus(t, b.request(str(l["public_url"]), p, "GET", nil), 404)
	}
	b.api("/auth/logout", "POST", Doc{}, 200)
	b.csrf = str(b.api("/auth/status", "GET", nil, 200)["csrf"])
	for range 5 {
		b.api("/auth/login", "POST", Doc{"username": "testadmin", "password": "incorrect"}, 401)
	}
	b.api("/auth/login", "POST", Doc{"username": "testadmin", "password": "incorrect"}, 429)
	// Public callbacks remain available without a management login.
	payload := Doc{"run_id": session["id"], "token": object(session["snapshot"])["token"], "data": "SYNTHETIC"}
	expectStatus(t, b.request(str(l["public_url"]), "/collect", "POST", payload), 201)
}
