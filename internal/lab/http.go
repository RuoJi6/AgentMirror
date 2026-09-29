package lab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const Version = "0.0.2-go"
const maxBody = 256 * 1024

type Options struct {
	DBPath, PublicURL, Host string
	AdminHost               string
	PublicPort, AdminPort   int
	Frontend                fs.FS
}
type App struct {
	store          *Store
	listeners      *listenerManager
	csrf           string
	auth           *adminAuth
	frontend       fs.FS
	closeOnce      sync.Once
	generationOnce sync.Once
	generation     *generationManager
}

func New(opts Options) (app *App, err error) {
	defer func() {
		if p := recover(); p != nil {
			if app != nil {
				app.Close()
			}
			app = nil
			err = caught(p)
		}
	}()
	if opts.DBPath == "" {
		opts.DBPath = "data/agentmirror-v2.sqlite3"
	}
	if opts.PublicURL == "" {
		opts.PublicURL = "http://10.211.55.2:8765"
	}
	if opts.Host == "" {
		opts.Host = "0.0.0.0"
	}
	if opts.PublicPort == 0 {
		opts.PublicPort = 8765
	}
	if opts.AdminPort == 0 {
		opts.AdminPort = 8766
	}
	if opts.AdminHost == "" {
		opts.AdminHost = "127.0.0.1"
	}
	if ip := net.ParseIP(opts.AdminHost); ip == nil || ip.To4() == nil || strings.Contains(opts.AdminHost, ":") {
		fail(400, "管理后台监听地址需为 IPv4 地址")
	}
	if opts.AdminPort < 1 || opts.AdminPort > 65535 || opts.PublicPort < 1 || opts.PublicPort > 65535 {
		fail(400, "端口必须是 1–65535 的整数")
	}
	store, err := openStore(opts.DBPath, opts.PublicURL)
	if err != nil {
		return nil, err
	}
	app = &App{store: store, csrf: randomToken(32), auth: newAdminAuth(), frontend: opts.Frontend}
	store.initAdminAuth()
	app.listeners = &listenerManager{app: app, servers: map[string]*runningServer{}, errors: map[string]string{}}
	app.listeners.initialize(opts)
	app.listeners.restore()
	app.generationManager()
	return app, nil
}
func (a *App) Close() {
	a.closeOnce.Do(func() {
		if a.listeners != nil {
			a.listeners.close()
		}
		if a.store != nil {
			a.closeGeneration()
			a.store.db.Close()
		}
	})
}
func (a *App) AdminURL() string  { return str(a.listeners.admin["public_url"]) }
func (a *App) PublicURL() string { return str(settings(a.store.db)["public_url"]) }
func (a *App) ListenerCount() int {
	a.listeners.mu.Lock()
	defer a.listeners.mu.Unlock()
	return len(a.listeners.servers)
}
func response(w http.ResponseWriter, r *http.Request, body any, status int, contentType string, headers map[string]string) {
	var encoded []byte
	switch b := body.(type) {
	case string:
		encoded = []byte(b)
	case []byte:
		encoded = b
	default:
		encoded = jsonBytes(body)
	}
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'")
	for key, value := range headers {
		w.Header().Set(key, value)
	}
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write(encoded)
	}
}
func requestBody(w http.ResponseWriter, r *http.Request) Doc {
	if len(r.TransferEncoding) > 0 {
		fail(400, "不支持分块请求")
	}
	if r.ContentLength > maxBody {
		fail(413, "请求正文不得超过 256 KiB")
	}
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		fail(415, "请使用 application/json")
	}
	reader := http.MaxBytesReader(w, r.Body, maxBody)
	defer reader.Close()
	content, readErr := io.ReadAll(reader)
	if readErr != nil {
		var max *http.MaxBytesError
		if errors.As(readErr, &max) {
			fail(413, "请求正文不得超过 256 KiB")
		}
		fail(400, "JSON 格式不正确")
	}
	if !utf8.Valid(content) {
		fail(400, "JSON 格式不正确")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			fail(413, "请求正文不得超过 256 KiB")
		}
		fail(400, "JSON 格式不正确")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		fail(400, "JSON 格式不正确")
	}
	result := object(value)
	if result == nil {
		fail(400, "需要 JSON 对象")
	}
	return result
}
func (a *App) handler(admin bool, listenerID string, port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !admin {
			var finish func()
			w, r, finish = a.store.trackAccess(w, r, listenerID, port)
			defer finish()
		}
		defer func() {
			if p := recover(); p != nil {
				status, message := 500, "服务处理失败，请检查服务日志"
				if e, ok := p.(problem); ok {
					status, message = e.status, e.message
				} else {
					log.Printf("request error: %T", p)
				}
				response(w, r, Doc{"error": message}, status, "", nil)
			}
		}()
		if admin {
			// Routers split trimmed paths, so reject noncanonical leading
			// slashes before deciding whether a request needs authentication.
			if strings.HasPrefix(r.URL.Path, "//") {
				fail(404, "路径不存在")
			}
			allowed := func(host string) bool {
				local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
				if !ok {
					return false
				}
				return host == net.JoinHostPort(local.IP.String(), fmtPort(port)) ||
					(local.IP.IsLoopback() && host == "localhost:"+fmtPort(port))
			}
			if !allowed(r.Host) {
				fail(403, "管理台仅接受当前监听地址")
			}
			if r.Method != "GET" && r.Method != "HEAD" {
				origin := r.Header.Get("Origin")
				if origin != "" && origin != "http://"+r.Host {
					fail(403, "请求来源不允许")
				}
			}
			if a.authRoute(w, r) {
				return
			}
			if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
				r = a.requireAdmin(r)
				if a.privateAuthRoute(w, r) {
					return
				}
			}
			a.adminRoute(w, r)
		} else {
			a.publicRoute(w, r, Doc{"id": listenerID, "port": port})
		}
	})
}
func (a *App) adminRoute(w http.ResponseWriter, r *http.Request) {
	if retiredAuthoringPath(r.URL.Path) {
		fail(410, "旧页面模板与旧部署流程已停用，请使用站点素材和蜜罐工作区；历史实验数据仍保留")
	}
	if a.accessFlowRoute(w, r) || a.hostedFileRoute(w, r) || a.mcpRoute(w, r) || a.agentRegistryRoute(w, r) || a.composerRoute(w, r) || a.generationRoute(w, r) {
		return
	}
	store := a.store
	p, method := r.URL.Path, r.Method
	query := r.URL.Query()
	send := func(v any) { response(w, r, v, 200, "", nil) }
	ok := func() { send(Doc{"ok": true}) }
	if p == "/api/overview" && method == "GET" {
		days, offset := overviewRange(r)
		send(store.overview(time.Now(), days, offset))
		return
	}
	if p == "/api/state" && method == "GET" {
		recent := array(store.sessions("", "", "", 1)["items"])
		if len(recent) > 5 {
			recent = recent[:5]
		}
		session := r.Context().Value(adminSessionKey{}).(*adminSession)
		deployments, legacy := deploymentLists(store.db)
		send(Doc{"csrf": session.csrf, "settings": settings(store.db), "runtime": a.listeners.runtime(), "listeners": a.listeners.listing(), "profiles": listing(store.db, "profiles"), "deployments": deployments, "legacy_deployments": legacy, "workspaces": workspaceListing(store.db), "stats": store.stats(), "recent": recent})
		return
	}
	if p == "/api/listeners" && method == "POST" {
		send(a.listeners.save(requestBody(w, r)))
		return
	}
	if p == "/api/settings" && method == "POST" {
		send(a.listeners.saveSettings(requestBody(w, r)))
		return
	}
	if p == "/api/collect-response" && method == "GET" {
		send(collectResponseConfig(settings(store.db)))
		return
	}
	if p == "/api/collect-response" && method == "POST" {
		send(store.saveCollectResponse(requestBody(w, r)))
		return
	}
	if p == "/api/collect-response/preview" && method == "POST" {
		config := validateCollectResponse(requestBody(w, r))
		send(renderCollectResponse(config, "PREVIEW_RUN", Doc{"ok": true, "receipt_id": 123}))
		return
	}
	if p == "/api/export" && method == "GET" {
		response(w, r, store.export(), 200, "", map[string]string{"Content-Disposition": `attachment; filename="agentmirror-sessions.json"`})
		return
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" {
		if parts[1] == "listeners" {
			if len(parts) == 4 && parts[3] == "default" && method == "POST" {
				a.listeners.setDefault(parts[2])
				ok()
				return
			}
			if len(parts) == 3 && method == "DELETE" {
				a.listeners.delete(parts[2])
				ok()
				return
			}
		}
		if len(parts) == 3 && method == "DELETE" && parts[1] == "profiles" {
			store.delete(parts[1], parts[2])
			ok()
			return
		}
		if len(parts) == 4 && parts[1] == "profiles" {
			if parts[3] == "versions" && method == "GET" {
				send(store.versions(parts[2]))
				return
			}
			if parts[3] == "preview" && method == "POST" {
				profile := requestBody(w, r)
				profile["fields"], profile["commands"] = validateSpec(profile)
				if _, valid := profile["body"].(string); !valid {
					fail(400, "提示词正文无效")
				}
				send(Doc{"instruction": renderInstruction(profile, Doc{"run_id": "PREVIEW_RUN", "token": "PREVIEW_TOKEN", "callback_url": "http://preview.invalid/collect"})})
				return
			}
		}
		if len(parts) == 5 && parts[1] == "sessions" && parts[3] == "reports" && method == "GET" {
			send(store.reportContent(parts[2], parts[4]))
			return
		}
		if len(parts) == 3 && parts[1] == "sessions" && method == "GET" {
			page := 0
			if value := query.Get("reports_page"); value != "" {
				var err error
				page, err = strconv.Atoi(value)
				if err != nil || page < 1 {
					fail(400, "页码无效")
				}
			}
			if query.Get("download") == "1" {
				response(w, r, store.session(parts[2]), 200, "", map[string]string{"Content-Disposition": "attachment; filename=session.json"})
				return
			}
			send(store.sessionView(parts[2], page))
			return
		}
		if len(parts) == 4 && parts[1] == "sessions" && parts[3] == "label" && method == "POST" {
			store.label(parts[2], requestBody(w, r))
			ok()
			return
		}
	}
	if len(parts) == 2 && parts[0] == "api" && method == "POST" && parts[1] == "profiles" {
		send(store.save(parts[1], requestBody(w, r)))
		return
	}
	if p == "/api/sessions" {
		if method == "DELETE" {
			send(store.clear())
			return
		}
		if method == "GET" {
			page := 1
			if query.Get("page") != "" {
				parsed, err := strconv.Atoi(query.Get("page"))
				if err != nil {
					fail(400, "页码无效")
				}
				page = parsed
			}
			send(store.sessions(query.Get("search"), query.Get("status"), query.Get("deployment"), page))
			return
		}
	}
	if strings.HasPrefix(p, "/api/") {
		fail(404, "接口不存在")
	}
	if method != "GET" && method != "HEAD" {
		fail(405, "方法不允许")
	}
	target := strings.TrimPrefix(p, "/")
	if target == "" {
		target = "index.html"
	}
	if !fs.ValidPath(target) || strings.Contains(target, "\\") {
		fail(404, "页面不存在")
	}
	if a.frontend == nil {
		response(w, r, "请先运行 npm run build 构建管理界面。", 503, "text/plain; charset=utf-8", nil)
		return
	}
	content, err := fs.ReadFile(a.frontend, target)
	if err != nil && !strings.Contains(path.Base(target), ".") {
		target = "index.html"
		content, err = fs.ReadFile(a.frontend, target)
	}
	if err != nil {
		fail(404, "资源不存在")
	}
	contentType := mime.TypeByExtension(path.Ext(target))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	response(w, r, content, 200, contentType, nil)
}
func unlocked(session Doc) bool {
	if fallback(object(object(session["snapshot"])["deployment"])["trigger"], "page_load") == "page_load" {
		return true
	}
	for _, e := range array(session["events"]) {
		if object(e)["kind"] == "interaction" {
			return true
		}
	}
	return false
}
func htmlCarriers(session Doc) []any {
	result := []any{}
	for _, c := range array(object(object(session["snapshot"])["deployment"])["carriers"]) {
		if strings.HasPrefix(str(c), "html_") {
			result = append(result, c)
		}
	}
	return result
}
func (s *Store) record(id, kind string, detail Doc) {
	s.write(func(q queryer) {
		if count(q, "SELECT COUNT(*) FROM sessions WHERE id=?", id) == 0 {
			fail(404, "会话不存在")
		}
		event(q, id, kind, detail)
	})
}
func (a *App) publicRoute(w http.ResponseWriter, r *http.Request, listener Doc) {
	store := a.store
	p, method := r.URL.Path, r.Method
	query := r.URL.Query()
	send := func(v any) { response(w, r, v, 200, "", nil) }
	sessionFor := func(id string) Doc {
		session := store.session(id)
		checkListener(object(session["snapshot"]), listener)
		store.write(func(q queryer) {
			rotateSessionToken(q, session)
			config := get(q, "listeners", str(listener["id"]))
			object(session["snapshot"])["callback_url"] = listenerCallbackURL(config, object(object(session["snapshot"])["deployment"]))
			refreshSessionPrompts(q, session)
		})
		accessSession(r, session)
		return session
	}
	newSession := func(slug, surface string) Doc {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		session := store.newSession(slug, ip, r.UserAgent(), p, surface, listener)
		accessSession(r, session)
		return session
	}
	config := get(store.db, "listeners", str(listener["id"]))
	deployment := entityMaybe(store.db, "deployments", str(config["deployment_id"]))
	accessNote(r, Doc{"deployment_id": config["deployment_id"], "deployment_name": deployment["name"], "listener": Doc{"id": config["id"], "name": config["name"], "port": config["port"], "public_url": config["public_url"], "site_node_id": config["site_node_id"]}})
	if value := listenerServerHeader(store.db, config, deployment); value != "" {
		w.Header().Set("Server", value)
	}
	if p == "/health" && (method == "GET" || method == "HEAD") {
		send(Doc{"status": "ok"})
		return
	}
	if p == callbackPath(deployment) && method == "POST" {
		accessNote(r, Doc{"callback_attempt": true, "callback_accepted": false})
		body := receiptBody(w, r)
		receipt, rejected := store.collectAttempt(body, config)
		// Only associate an existing session that belongs to this listener.
		if count(store.db, "SELECT COUNT(*) FROM sessions WHERE id=? AND json_extract(snapshot,'$.listener.id')=?", str(body["run_id"]), config["id"]) > 0 {
			accessNote(r, Doc{"session_id": body["run_id"]})
		}
		accessNote(r, Doc{"callback_accepted": !rejected && receipt["ok"] == true, "report_id": receipt["receipt_id"]})
		var result Doc
		if rejected {
			result = renderCollectRejectedResponse(collectRejectedResponseConfig(deployment), str(body["run_id"]))
		} else if receipt["ok"] == false {
			result = Doc{"status": 422, "content_type": "application/json; charset=utf-8", "body": dump(receipt)}
		} else {
			result = renderCollectResponse(collectResponseConfig(settings(store.db)), str(body["run_id"]), receipt)
		}
		headers := map[string]string{}
		for key, value := range object(result["headers"]) {
			headers[http.CanonicalHeaderKey(key)] = str(value)
		}
		if validation := object(receipt["validation"]); validation != nil {
			headers["X-Receipt-Schema"] = str(validation["schema"])
			headers["X-Execution-Evidence"] = "unverified"
		}
		response(w, r, str(result["body"]), integer(result["status"]), str(result["content_type"]), headers)
		return
	}
	if a.composerPublicRoute(w, r, listener) {
		return
	}
	// Compatibility-only routes below serve already-bound legacy listeners and
	// historical snapshots. New authoring/publishing is handled by workspaces.
	if p == "/portal/api/session" && method == "POST" {
		body := requestBody(w, r)
		if body["action"] != "login" {
			fail(400, "未知操作")
		}
		session := sessionFor(query.Get("run_id"))
		if object(object(session["snapshot"])["template"])["kind"] != "login" {
			fail(400, "此页面没有登录流程")
		}
		store.record(str(session["id"]), "interaction", Doc{"action": "simulated_login"})
		send(Doc{"ok": true, "next": "/portal/workspace?run_id=" + str(session["id"])})
		return
	}
	if method != "GET" && method != "HEAD" {
		fail(404, "接口不存在")
	}
	if p == "/robots.txt" {
		response(w, r, "User-agent: *\nDisallow: /\n", 200, "text/plain; charset=utf-8", nil)
		return
	}
	if p == "/" {
		config := get(store.db, "listeners", str(listener["id"]))
		if config["root_surface"] != "page" {
			config = surfaceConfig(str(config["root_surface"]))
			response(w, r, "", 302, "text/plain", map[string]string{"Location": str(config["path"])})
			return
		}
	}
	if route := resolveSurface(p); route != nil {
		run := query.Get("run_id")
		var session Doc
		if run != "" {
			session = sessionFor(run)
			snap := object(session["snapshot"])
			if object(snap["surface"])["kind"] != route.kind || (route.slug != "" && object(snap["deployment"])["slug"] != route.slug) {
				fail(403, "会话与暴露面不匹配")
			}
			store.record(run, "request", Doc{"path": p})
		} else {
			session = newSession(route.slug, route.kind)
		}
		rep := renderSurface(route, session)
		if rep.carrier != "" && method != "HEAD" {
			store.recordLegacyPrompt(session, "delivery", Doc{"path": p, "carriers": []any{rep.carrier}})
			accessLegacyPrompt(r, session, "delivery")
		}
		rep.headers["X-Run-ID"] = str(session["id"])
		response(w, r, rep.body, 200, rep.mime, rep.headers)
		return
	}
	if p == "/" || p == "/admin/login" || (strings.HasPrefix(p, "/h/") && len(strings.Split(strings.Trim(p, "/"), "/")) == 2) {
		slug := ""
		if strings.HasPrefix(p, "/h/") {
			slug = path.Base(p)
		}
		session := newSession(slug, "")
		carriers := htmlCarriers(session)
		if method != "HEAD" && unlocked(session) && len(carriers) > 0 {
			store.recordLegacyPrompt(session, "delivery", Doc{"path": p, "carriers": carriers})
			accessLegacyPrompt(r, session, "delivery")
		}
		response(w, r, page(object(object(session["snapshot"])["template"]), session, false, false), 200, "text/html; charset=utf-8", map[string]string{"X-Run-ID": str(session["id"])})
		return
	}
	if p == "/portal/api/login-config" || p == "/portal/api/content" || p == "/assets/client.js" || p == "/portal/workspace" {
		id := query.Get("run_id")
		var session Doc
		if id == "" && strings.HasPrefix(p, "/portal/api/") {
			session = newSession("", "")
		} else {
			session = sessionFor(id)
		}
		open := unlocked(session)
		snap := object(session["snapshot"])
		template := object(snap["template"])
		deployment := object(snap["deployment"])
		if p == "/portal/workspace" {
			if !open || template["kind"] != "login" {
				fail(403, "请先完成登录流程")
			}
			carriers := htmlCarriers(session)
			if method != "HEAD" && len(carriers) > 0 {
				store.recordLegacyPrompt(session, "delivery", Doc{"path": p, "carriers": carriers})
				accessLegacyPrompt(r, session, "delivery")
			}
			response(w, r, page(template, session, false, true), 200, "text/html; charset=utf-8", nil)
			return
		}
		carrier := "api"
		if p == "/assets/client.js" {
			carrier = "js"
		}
		includeInstruction := open && has(deployment["carriers"], carrier)
		deliver := method != "HEAD" && includeInstruction
		eventKind := "request"
		delivered := []any{}
		if deliver {
			eventKind = "delivery"
			delivered = append(delivered, carrier)
		}
		store.recordLegacyPrompt(session, eventKind, Doc{"path": p, "carriers": delivered, "locked": !open})
		accessLegacyPrompt(r, session, eventKind)
		if carrier == "js" {
			response(w, r, clientJS(session, open), 200, "application/javascript; charset=utf-8", nil)
			return
		}
		config := Doc{"service": template["brand"], "version": "1.0", "run_id": session["id"], "authentication_required": !open}
		if includeInstruction {
			config["client_api"] = Doc{"instruction": snap["instruction"]}
		}
		response(w, r, config, 200, "", map[string]string{"X-Run-ID": str(session["id"])})
		return
	}
	fail(404, "页面不存在")
}
func (a *App) Banner() string {
	return fmt.Sprintf("AgentMirror %s\n共 %d 个监听（管理后台由启动参数配置）\n管理端 %s\n公告地址 %s\n", Version, a.ListenerCount(), a.AdminURL(), a.PublicURL())
}

// JSON envelopes and plain text share the same authenticated receiver. The
// latter identifies its session with headers, never with user-authored schema.
func receiptBody(w http.ResponseWriter, r *http.Request) Doc {
	kind := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	var body Doc
	if kind == "application/json" {
		body = requestBody(w, r)
	} else if kind == "text/plain" {
		if len(r.TransferEncoding) > 0 {
			fail(400, "不支持分块请求")
		}
		if r.ContentLength > maxBody {
			fail(413, "请求正文不得超过 256 KiB")
		}
		reader := http.MaxBytesReader(w, r.Body, maxBody)
		defer reader.Close()
		content, err := io.ReadAll(reader)
		if err != nil {
			fail(413, "请求正文不得超过 256 KiB")
		}
		if !utf8.Valid(content) {
			fail(400, "回传内容需为 UTF-8 文本")
		}
		body = Doc{"data": string(content)}
	} else {
		fail(415, "回传请使用 application/json 或 text/plain")
	}
	for key, header := range map[string]string{"run_id": "X-Run-ID", "token": "X-Run-Token"} {
		if value := r.Header.Get(header); value != "" {
			if current, exists := body[key]; exists && str(current) != value {
				fail(400, "会话信息不一致")
			}
			body[key] = value
		}
	}
	return body
}
