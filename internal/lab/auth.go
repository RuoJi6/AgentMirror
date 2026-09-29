package lab

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const adminSessionTTL = 12 * time.Hour

type adminSession struct {
	hash, csrf, username string
	expires              int64
}
type adminSessionKey struct{}
type loginAttempt struct {
	count int
	until time.Time
}
type adminAuth struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	slots    chan struct{}
}

func newAdminAuth() *adminAuth {
	return &adminAuth{attempts: map[string]loginAttempt{}, slots: make(chan struct{}, 2)}
}
func (s *Store) initAdminAuth() {
	// Credentials and login sessions are separate from exported application data.
	exec(s.db, `CREATE TABLE IF NOT EXISTS admin_account(id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL, password_hash TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS admin_sessions(token_hash TEXT PRIMARY KEY, csrf TEXT NOT NULL, expires_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS admin_session_expiry ON admin_sessions(expires_at);`)
}
func (s *Store) adminCredentials() (username, hash string) {
	err := s.db.QueryRow("SELECT username,password_hash FROM admin_account WHERE id=1").Scan(&username, &hash)
	if err != sql.ErrNoRows {
		check(err)
	}
	return
}
func validAdminPassword(value any) string {
	password, ok := value.(string)
	if !ok || !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || utf8.RuneCountInString(password) > 128 {
		fail(400, "密码需为 12–128 个字符")
	}
	return password
}
func encodeAdminPassword(password string) string {
	salt, err := base64.RawURLEncoding.DecodeString(randomToken(16))
	check(err)
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return "argon2id-v1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
}
func verifyAdminPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id-v1" || len(password) > 512 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[1])
	want, e2 := base64.RawStdEncoding.DecodeString(parts[2])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(want) != 32 {
		return false
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(key, want) == 1
}
func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (a *App) adminCookieName() string {
	return "agentmirror_admin_" + fmtPort(integer(a.listeners.admin["port"]))
}
func (a *App) readAdminSession(r *http.Request) *adminSession {
	cookie, err := r.Cookie(a.adminCookieName())
	if err != nil || len(cookie.Value) != 43 {
		return nil
	}
	session := &adminSession{hash: tokenHash(cookie.Value)}
	err = a.store.db.QueryRow(`SELECT s.csrf,s.expires_at,a.username FROM admin_sessions s JOIN admin_account a ON a.id=1 WHERE s.token_hash=? AND s.expires_at>?`, session.hash, time.Now().Unix()).Scan(&session.csrf, &session.expires, &session.username)
	if err == sql.ErrNoRows {
		return nil
	}
	check(err)
	return session
}
func (a *App) writeAdminCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	age := int(adminSessionTTL.Seconds())
	if token == "" {
		age = -1
	}
	http.SetCookie(w, &http.Cookie{Name: a.adminCookieName(), Value: token, Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: age, Expires: expires})
}
func newSessionValues() (token, csrf string, expires int64) {
	return randomToken(32), randomToken(32), time.Now().Add(adminSessionTTL).Unix()
}
func insertAdminSession(q queryer, token, csrf string, expires int64) {
	exec(q, "DELETE FROM admin_sessions WHERE expires_at<=?", time.Now().Unix())
	exec(q, "INSERT INTO admin_sessions VALUES(?,?,?)", tokenHash(token), csrf, expires)
	// Bound active sessions even if clients repeatedly sign in without signing out.
	exec(q, "DELETE FROM admin_sessions WHERE token_hash IN (SELECT token_hash FROM admin_sessions ORDER BY expires_at DESC LIMIT -1 OFFSET 20)")
}
func (a *App) authenticated(w http.ResponseWriter, r *http.Request, username, token, csrf string, expires int64) {
	a.writeAdminCookie(w, r, token, time.Unix(expires, 0))
	response(w, r, Doc{"initialized": true, "authenticated": true, "username": username, "csrf": csrf, "expires_at": expires}, 200, "", nil)
}
func (a *adminAuth) startAttempt(r *http.Request) func(bool) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.mu.Lock()
	now := time.Now()
	for key, entry := range a.attempts {
		if !entry.until.After(now) {
			delete(a.attempts, key)
		}
	}
	entry := a.attempts[ip]
	if entry.count >= 5 || (entry.count == 0 && len(a.attempts) >= 1024) {
		a.mu.Unlock()
		fail(429, "尝试次数过多，请 5 分钟后重试")
	}
	entry.count++
	if entry.until.IsZero() {
		entry.until = now.Add(5 * time.Minute)
	}
	a.attempts[ip] = entry
	a.mu.Unlock()
	select {
	case a.slots <- struct{}{}:
	default:
		fail(429, "登录请求较多，请稍后重试")
	}
	return func(success bool) {
		<-a.slots
		if success {
			a.mu.Lock()
			delete(a.attempts, ip)
			a.mu.Unlock()
		}
	}
}
func checkAdminCSRF(r *http.Request, csrf string) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Token")), []byte(csrf)) != 1 {
		fail(403, "管理会话已失效，请刷新页面")
	}
}

// Only these three exact endpoints are available before authentication.
func (a *App) authRoute(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if p == "/api/auth/status" && r.Method == "GET" {
		_, hash := a.store.adminCredentials()
		status := Doc{"initialized": hash != "", "authenticated": false, "csrf": a.csrf}
		if session := a.readAdminSession(r); session != nil {
			merge(status, Doc{"authenticated": true, "username": session.username, "csrf": session.csrf, "expires_at": session.expires})
		}
		response(w, r, status, 200, "", nil)
		return true
	}
	if (p != "/api/auth/setup" && p != "/api/auth/login") || r.Method != "POST" {
		return false
	}
	preloginCSRF := a.csrf
	if session := a.readAdminSession(r); session != nil {
		preloginCSRF = session.csrf
	}
	checkAdminCSRF(r, preloginCSRF)
	raw := requestBody(w, r)
	done := a.auth.startAttempt(r)
	success := false
	defer func() { done(success) }()
	username, hash := a.store.adminCredentials()
	token, csrf, expires := newSessionValues()
	if p == "/api/auth/setup" {
		if hash != "" {
			fail(409, "管理员已设置，请登录")
		}
		username = strings.TrimSpace(str(raw["username"]))
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`).MatchString(username) {
			fail(400, "用户名需为 3–32 位字母、数字、点、下划线或短横线")
		}
		password := validAdminPassword(raw["password"])
		if raw["confirm_password"] != password {
			fail(400, "两次输入的密码不一致")
		}
		hash = encodeAdminPassword(password)
		a.store.write(func(q queryer) {
			if count(q, "SELECT COUNT(*) FROM admin_account") != 0 {
				fail(409, "管理员已设置，请登录")
			}
			exec(q, "INSERT INTO admin_account VALUES(1,?,?,?)", username, hash, timestamp())
			insertAdminSession(q, token, csrf, expires)
		})
	} else {
		if hash == "" {
			fail(409, "请先完成管理员初始化")
		}
		password, _ := raw["password"].(string)
		passwordOK := verifyAdminPassword(hash, password)
		if subtle.ConstantTimeCompare([]byte(str(raw["username"])), []byte(username)) != 1 || !passwordOK {
			fail(401, "用户名或密码错误")
		}
		a.store.write(func(q queryer) {
			// A simultaneous password change must not re-enable an old credential.
			if count(q, "SELECT COUNT(*) FROM admin_account WHERE password_hash=?", hash) == 0 {
				fail(401, "密码已变更，请重新登录")
			}
			insertAdminSession(q, token, csrf, expires)
		})
	}
	success = true
	a.authenticated(w, r, username, token, csrf, expires)
	return true
}
func (a *App) requireAdmin(r *http.Request) *http.Request {
	session := a.readAdminSession(r)
	if session == nil {
		fail(401, "请先登录管理后台")
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		checkAdminCSRF(r, session.csrf)
	}
	return r.WithContext(context.WithValue(r.Context(), adminSessionKey{}, session))
}
func (a *App) privateAuthRoute(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if (p != "/api/auth/logout" && p != "/api/auth/password") || r.Method != "POST" {
		return false
	}
	session := r.Context().Value(adminSessionKey{}).(*adminSession)
	if p == "/api/auth/logout" {
		exec(a.store.db, "DELETE FROM admin_sessions WHERE token_hash=?", session.hash)
		a.writeAdminCookie(w, r, "", time.Unix(1, 0))
		response(w, r, Doc{"ok": true}, 200, "", nil)
		return true
	}
	raw := requestBody(w, r)
	password := validAdminPassword(raw["new_password"])
	if raw["confirm_password"] != password {
		fail(400, "两次输入的密码不一致")
	}
	done := a.auth.startAttempt(r)
	success := false
	defer func() { done(success) }()
	username, hash := a.store.adminCredentials()
	if !verifyAdminPassword(hash, str(raw["current_password"])) {
		fail(400, "当前密码错误")
	}
	encoded := encodeAdminPassword(password)
	token, csrf, expires := newSessionValues()
	a.store.write(func(q queryer) {
		if count(q, "SELECT COUNT(*) FROM admin_account WHERE password_hash=?", hash) == 0 {
			fail(409, "密码已变更，请重新登录")
		}
		exec(q, "UPDATE admin_account SET password_hash=?,updated_at=? WHERE id=1", encoded, timestamp())
		exec(q, "DELETE FROM admin_sessions")
		insertAdminSession(q, token, csrf, expires)
	})
	success = true
	a.authenticated(w, r, username, token, csrf, expires)
	return true
}
