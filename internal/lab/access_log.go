package lab

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"
)

// Public traffic is recorded separately from session ownership: a cookie or
// callback token reused from another IP must not change the observed source.
type accessContextKey struct{}
type accessCapture struct{ detail Doc }
type accessResponseWriter struct {
	http.ResponseWriter
	status, bytes int
}

func (w *accessResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *accessResponseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *accessResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += n
	return n, err
}

func accessNote(r *http.Request, detail Doc) {
	if capture, ok := r.Context().Value(accessContextKey{}).(*accessCapture); ok {
		merge(capture.detail, detail)
	}
}
func accessSession(r *http.Request, session Doc) {
	snap := object(session["snapshot"])
	deployment := object(snap["deployment"])
	accessNote(r, Doc{"session_id": session["id"], "deployment_id": deployment["id"], "deployment_name": deployment["name"]})
}
func accessLegacyPrompt(r *http.Request, session Doc, kind string) {
	if kind == "delivery" && r.Method != "HEAD" {
		profile := object(object(session["snapshot"])["profile"])
		accessNote(r, Doc{"deliveries": []any{Doc{"profile_id": profile["id"], "profile_name": profile["name"], "profile_version": profile["version"]}}})
	}
}
func accessComposerResult(r *http.Request, result Doc) {
	data := Doc{}
	for _, key := range []string{"rule_matched", "rule_id", "binding_id", "site_node_id", "site_version_id", "release_id", "fallback"} {
		data[key] = result[key]
	}
	if r.Method != "HEAD" {
		data["deliveries"] = result["deliveries"]
	}
	accessNote(r, data)
}

func (s *Store) trackAccess(w http.ResponseWriter, r *http.Request, listenerID string, port int) (http.ResponseWriter, *http.Request, func()) {
	started := time.Now()
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if parsed := net.ParseIP(ip); parsed != nil {
		ip = parsed.String()
	}
	detail := Doc{"kind": "request", "method": r.Method, "path": boundedText(r.URL.RequestURI(), 8192), "host": boundedText(r.Host, 512), "referer": boundedText(r.Referer(), 2048), "listener": Doc{"id": listenerID, "port": port}, "source": "http_request"}
	r = r.WithContext(context.WithValue(r.Context(), accessContextKey{}, &accessCapture{detail: detail}))
	writer := &accessResponseWriter{ResponseWriter: w}
	return writer, r, func() {
		// Logging is observational and must never emit a second HTTP response.
		defer func() {
			if p := recover(); p != nil {
				log.Printf("access log error: %T", p)
			}
		}()
		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}
		merge(detail, Doc{"status": status, "response_bytes": writer.bytes, "duration_ms": time.Since(started).Milliseconds(), "content_type": w.Header().Get("Content-Type"), "location": boundedText(w.Header().Get("Location"), 2048)})
		s.write(func(q queryer) {
			exec(q, `INSERT INTO access_requests(at,ip,ua,listener_id,deployment_id,session_id,delivered,received,detail) VALUES(?,?,?,?,?,?,?,?,?)`,
				started.UTC().Format("2006-01-02T15:04:05.000+00:00"), ip, boundedText(r.UserAgent(), 1000), listenerID, str(detail["deployment_id"]), str(detail["session_id"]), len(array(detail["deliveries"])), boolean(detail["callback_accepted"]), dump(detail))
		})
	}
}

func (s *Store) initAccessLog() {
	s.write(func(q queryer) {
		exec(q, `CREATE TABLE IF NOT EXISTS access_requests(id INTEGER PRIMARY KEY, at TEXT NOT NULL, ip TEXT NOT NULL, ua TEXT NOT NULL DEFAULT '', listener_id TEXT NOT NULL DEFAULT '', deployment_id TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '', delivered INTEGER NOT NULL DEFAULT 0, received INTEGER NOT NULL DEFAULT 0, detail TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS access_ip_time ON access_requests(ip,at,id);
CREATE INDEX IF NOT EXISTS access_deployment_time ON access_requests(deployment_id,at,id);
CREATE TABLE IF NOT EXISTS access_log_meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);`)
		if count(q, "SELECT COUNT(*) FROM access_log_meta WHERE key='legacy_import'") > 0 {
			return
		}
		// Import only evidence actually retained by the old version. In particular
		// a historical session's IP is not advertised as a per-request IP reading.
		exec(q, `INSERT INTO access_requests(at,ip,ua,listener_id,deployment_id,session_id,delivered,received,detail)
SELECT e.at,coalesce(s.ip,''),coalesce(s.ua,''),coalesce(json_extract(s.snapshot,'$.listener.id'),''),coalesce(s.deployment_id,''),s.id,
CASE WHEN e.kind='delivery' THEN 1 ELSE 0 END, CASE WHEN e.kind='receipt' THEN 1 ELSE 0 END,
json_object('kind',e.kind,'source','legacy_session','legacy_event_id',e.id,
'path',json_extract(e.detail,'$.path'),'method',json_extract(e.detail,'$.method'),
'deployment_name',json_extract(s.snapshot,'$.deployment.name'),'listener',json_extract(s.snapshot,'$.listener'),
'deliveries',json_extract(e.detail,'$.deliveries'),'rule_id',json_extract(e.detail,'$.rule_id'),
'report_id',json_extract(e.detail,'$.report_id'),'filename',json_extract(e.detail,'$.filename'),
'bytes_sent',json_extract(e.detail,'$.bytes_sent'))
FROM events e JOIN sessions s ON s.id=e.session_id
WHERE e.kind IN ('visit','request','delivery','receipt','file_download') ORDER BY e.id`)
		exec(q, "INSERT INTO access_log_meta VALUES('legacy_import',?)", timestamp())
	})
}
