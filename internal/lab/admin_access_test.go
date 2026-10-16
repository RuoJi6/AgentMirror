package lab

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminExternalOriginLogin(t *testing.T) {
	b := newUnauthedLab(t)
	b.opts.AdminOrigins = []string{"https://admin.example.test", "http://198.51.100.20:18766"}
	reopen := func() {
		t.Helper()
		b.client.CloseIdleConnections()
		b.app.Close()
		var err error
		b.app, err = New(b.opts)
		if err != nil {
			t.Fatal(err)
		}
		b.admin = b.app.AdminURL()
	}
	reopen()
	host, origin := "admin.example.test", "https://admin.example.test"
	var cookie *http.Cookie
	request := func(method, path, requestOrigin, token string, body Doc, want int) Doc {
		t.Helper()
		req, err := http.NewRequest(method, b.admin+path, bytes.NewReader(jsonBytes(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host // Emulate a proxy preserving the external Host.
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", requestOrigin)
		req.Header.Set("X-Admin-Token", token)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res, err := b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s %s: got %d, want %d", method, path, res.StatusCode, want)
		}
		for _, value := range res.Cookies() {
			if value.MaxAge > 0 {
				cookie = value
			}
		}
		return decodeBodyForAdminTest(t, res)
	}
	status := request("GET", "/api/auth/status", "", "", nil, 200)
	credentials := Doc{"username": "remoteadmin", "password": "SYNTHETIC-remote-password", "confirm_password": "SYNTHETIC-remote-password"}
	setup := request("POST", "/api/auth/setup", origin, str(status["csrf"]), credentials, 200)
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("HTTPS proxy login did not set secure session cookie", cookie)
	}
	request("GET", "/api/state", "", "", nil, 200)
	request("POST", "/api/auth/logout", "http://198.51.100.20:18766", str(setup["csrf"]), Doc{}, 403)
	request("POST", "/api/auth/logout", origin, "bad-token", Doc{}, 403)
	request("POST", "/api/auth/logout", origin, str(setup["csrf"]), Doc{}, 200)
	cookie = nil
	request("GET", "/api/state", "", "", nil, 401)
	status = request("GET", "/api/auth/status", "", "", nil, 200)
	request("POST", "/api/auth/login", origin, str(status["csrf"]), credentials, 200)
	request("GET", "/api/state", "", "", nil, 200)
	if !strings.Contains(b.app.Banner(), origin) || !strings.Contains(b.app.Banner(), "管理监听 0.0.0.0:") {
		t.Fatal("startup output omitted remote management configuration")
	}
	// Removing a startup origin revokes it; it is never persisted in SQLite.
	b.opts.AdminOrigins = nil
	reopen()
	request("GET", "/api/auth/status", "", "", nil, 403)
}

func decodeBodyForAdminTest(t *testing.T, res *http.Response) Doc {
	t.Helper()
	var body bytes.Buffer
	if _, err := body.ReadFrom(res.Body); err != nil {
		t.Fatal(err)
	}
	return decode(body.String())
}

func TestAdminRemoteHostPolicy(t *testing.T) {
	access, err := newAdminAccess([]string{"https://ADMIN.example.test:443/", "http://198.51.100.20:18766"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, local, host, origin string
		allowed, sameOrigin       bool
	}{
		{"direct remote IP", "192.0.2.10", "192.0.2.10:8766", "http://192.0.2.10:8766", true, true},
		{"loopback", "127.0.0.1", "localhost:8766", "http://localhost:8766", true, true},
		{"external domain", "127.0.0.1", "admin.example.test", "https://admin.example.test", true, true},
		{"external NAT port", "192.0.2.10", "198.51.100.20:18766", "http://198.51.100.20:18766", true, true},
		{"unconfigured host", "192.0.2.10", "evil.invalid:8766", "http://evil.invalid:8766", false, true},
		{"unconfigured public IP", "192.0.2.10", "203.0.113.10:8766", "http://203.0.113.10:8766", false, true},
		{"other allowed origin", "127.0.0.1", "admin.example.test", "http://198.51.100.20:18766", true, false},
		{"foreign origin", "127.0.0.1", "admin.example.test", "https://evil.invalid", true, false},
		{"unconfigured HTTPS", "192.0.2.10", "192.0.2.10:8766", "https://192.0.2.10:8766", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://"+tc.host+"/api/auth/login", nil)
			req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP(tc.local), Port: 8766}))
			req.Header.Set("X-Forwarded-Host", "admin.example.test")
			req.Header.Set("X-Forwarded-Proto", "https")
			if access.allowsHost(req, 8766) != tc.allowed || access.sameOrigin(req, tc.origin) != tc.sameOrigin {
				t.Fatal("incorrect management host/origin policy")
			}
		})
	}
	req := httptest.NewRequest("POST", "https://192.0.2.10:8766/api/auth/login", nil)
	req.TLS = &tls.ConnectionState{}
	if !access.sameOrigin(req, "https://192.0.2.10:8766") || !access.secure(req) {
		t.Fatal("native HTTPS rejected")
	}
}

func TestAdminOriginStartupValidation(t *testing.T) {
	for _, origin := range []string{"", "*", "ftp://admin.example.test", "https://*.example.test", "https://user:pass@admin.example.test", "https://admin.example.test/path", "https://admin.example.test?query=1", "https://admin.example.test#fragment", "https://admin.example.test:0", "http://admin.example.test:65536"} {
		t.Run(origin, func(t *testing.T) {
			if _, err := newAdminAccess([]string{origin}); err == nil {
				t.Fatalf("accepted invalid origin %q", origin)
			}
		})
	}
	if _, err := New(Options{DBPath: filepath.Join(t.TempDir(), "must-not-create.sqlite3"), AdminOrigins: []string{"*"}}); err == nil {
		t.Fatal("invalid origin did not fail startup")
	}
}
