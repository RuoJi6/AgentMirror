package lab

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const accessFlowPageSize = 100

func accessFilters(values url.Values, exact bool) (string, []any, int) {
	conditions, args := []string{"1=1"}, []any{}
	page := 1
	if raw := values.Get("page"); raw != "" {
		var err error
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 || page > 100000000 {
			fail(400, "页码无效")
		}
	}
	if exact {
		ip := values.Get("ip")
		if net.ParseIP(ip) == nil {
			fail(400, "请选择有效的来源 IP")
		}
		conditions, args = append(conditions, "ip=?"), append(args, ip)
	} else if search := strings.TrimSpace(values.Get("search")); search != "" {
		conditions, args = append(conditions, "(ip LIKE ? OR ua LIKE ?)"), append(args, "%"+search+"%", "%"+search+"%")
	}
	if deployment := values.Get("deployment"); deployment != "" {
		conditions, args = append(conditions, "deployment_id=?"), append(args, deployment)
	}
	if values.Has("ua") {
		conditions, args = append(conditions, "ua=?"), append(args, values.Get("ua"))
	}
	var start, end time.Time
	for _, key := range []string{"from", "to"} {
		if raw := values.Get(key); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				fail(400, "时间范围格式不正确")
			}
			operator := ">="
			if key == "to" {
				operator, end = "<=", parsed
			} else {
				start = parsed
			}
			conditions, args = append(conditions, "at "+operator+" ?"), append(args, parsed.UTC().Format("2006-01-02T15:04:05.000+00:00"))
		}
	}
	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		fail(400, "结束时间不能早于开始时间")
	}
	return " WHERE " + strings.Join(conditions, " AND "), args, page
}

func (s *Store) accessSources(values url.Values) Doc {
	where, args, page := accessFilters(values, false)
	var result Doc
	s.write(func(q queryer) {
		total := count(q, "SELECT COUNT(DISTINCT ip) FROM access_requests"+where, args...)
		items := rows(q, `SELECT ip,COUNT(*) AS record_count,COUNT(DISTINCT NULLIF(session_id,'')) AS session_count,
COUNT(DISTINCT ua) AS user_agent_count,COUNT(DISTINCT listener_id) AS listener_count,
SUM(delivered) AS delivery_count,SUM(received) AS report_count,MIN(at) AS first_seen,MAX(at) AS last_seen
FROM access_requests`+where+" GROUP BY ip ORDER BY last_seen DESC,ip LIMIT 20 OFFSET ?", append(args, (page-1)*20)...)
		result = Doc{"items": items, "total": total, "page": page, "page_size": 20}
	})
	return result
}

func (s *Store) accessFlow(values url.Values) Doc {
	where, args, page := accessFilters(values, true)
	var result Doc
	s.write(func(q queryer) {
		before := 0
		if raw := values.Get("before"); raw != "" {
			var err error
			before, err = strconv.Atoi(raw)
			if err != nil || before < 0 {
				fail(400, "分页快照无效")
			}
		} else {
			before = integer(rows(q, "SELECT coalesce(MAX(id),0) AS id FROM access_requests")[0]["id"])
		}
		where, args = where+" AND id<=?", append(args, before)
		total := count(q, "SELECT COUNT(*) FROM access_requests"+where, args...)
		items := rows(q, "SELECT * FROM access_requests"+where+" ORDER BY at DESC,id DESC LIMIT ? OFFSET ?", append(args, accessFlowPageSize, (page-1)*accessFlowPageSize)...)
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
		// Keep all UA choices available when one UA is selected.
		allUA := url.Values{}
		for key, values := range values {
			allUA[key] = append([]string{}, values...)
		}
		allUA.Del("ua")
		uaWhere, uaArgs, _ := accessFilters(allUA, true)
		agents := rows(q, "SELECT ua,COUNT(*) AS record_count FROM access_requests"+uaWhere+" GROUP BY ua ORDER BY record_count DESC,ua LIMIT 100", uaArgs...)
		result = Doc{"ip": values.Get("ip"), "total": total, "page": page, "page_size": accessFlowPageSize, "snapshot_id": before, "user_agents": agents, "access_flow": observedAccessFlow(items, values.Get("ip"))}
	})
	return result
}

func observedAccessFlow(items []Doc, ip string) Doc {
	groups, nodes, edges := []any{}, []any{}, []any{}
	seen := map[string]bool{}
	previous := ""
	for _, row := range items {
		d := decode(str(row["detail"]))
		group := str(row["session_id"])
		if group == "" {
			group = "unlinked-" + str(row["listener_id"])
		}
		if !seen[group] {
			label := str(d["deployment_name"])
			if label == "" {
				label = "未关联工作区"
			}
			if str(row["session_id"]) != "" {
				label += " · 会话 " + boundedText(str(row["session_id"]), 16)
			} else {
				label += " · 未关联会话"
			}
			groups = append(groups, Doc{"id": group, "kind": "traffic", "label": label})
			seen[group] = true
		}
		evidence := clone(d)
		for _, key := range []string{"at", "ip", "ua", "session_id", "listener_id", "deployment_id"} {
			evidence[key] = row[key]
		}
		if d["source"] == "legacy_session" {
			evidence["source_note"] = "历史会话事件：来源 IP 与 User-Agent 来自会话创建时记录；未记录的请求信息无法补齐。"
		}
		id := fmt.Sprintf("access-%d", integer(row["id"]))
		kind, label, status := "request", strings.TrimSpace(str(d["method"])+" "+str(d["path"])), "observed"
		if integer(d["status"]) >= 400 {
			status = "fail"
		}
		switch d["kind"] {
		case "visit":
			kind, label = "step", "会话开始 · "+str(d["path"])
		case "delivery":
			kind, label = "delivery", "提示词交付 · "+str(d["path"])
			if profiles := array(d["deliveries"]); len(profiles) > 0 {
				merge(evidence, object(profiles[0]))
			}
		case "receipt":
			kind, label = "callback", fmt.Sprintf("回传 #%d", integer(d["report_id"]))
		case "file_download":
			kind, label = "download", "下载 · "+str(d["filename"])
		}
		if label == "" {
			label = "历史访问记录"
		}
		nodes = append(nodes, Doc{"id": id, "group": group, "kind": kind, "label": boundedText(label, 220), "status": status, "evidence": evidence})
		if previous != "" {
			edges = append(edges, Doc{"source": previous, "target": id, "relation": "observed_next"})
		}
		previous = id
		add := func(suffix, kind, label, state, relation string, fields Doc) string {
			child := id + "-" + suffix
			detail := clone(evidence)
			merge(detail, fields)
			nodes = append(nodes, Doc{"id": child, "group": group, "kind": kind, "label": boundedText(label, 220), "status": state, "evidence": detail})
			edges = append(edges, Doc{"source": id, "target": child, "relation": relation})
			return child
		}
		if boolean(d["rule_matched"]) {
			add("rule", "rule", "命中规则 · "+str(d["rule_id"]), "observed", "matched", Doc{})
		}
		if d["kind"] != "delivery" {
			for i, raw := range array(d["deliveries"]) {
				profile := object(raw)
				add(fmt.Sprintf("delivery-%d", i), "delivery", "提示词交付 · "+str(profile["profile_name"]), "observed", "delivered", profile)
			}
		}
		if download := object(d["download"]); download != nil {
			add("download", "download", "下载 · "+str(download["filename"]), "observed", "downloaded", download)
		}
		if boolean(d["callback_attempt"]) {
			label, state, relation := "回传被拒绝", "fail", "callback_attempt"
			if boolean(d["callback_accepted"]) {
				label, state, relation = fmt.Sprintf("已接收回传 #%d", integer(d["report_id"])), "observed", "received"
			}
			add("callback", "callback", label, state, relation, Doc{})
		}
	}
	return Doc{"version": 1, "environment": "observed_traffic", "source_ip": ip, "groups": groups, "nodes": nodes, "edges": edges, "truncated": false}
}

func (a *App) accessFlowRoute(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/session-sources" && r.URL.Path != "/api/session-sources/flow" {
		return false
	}
	if r.Method != "GET" {
		fail(405, "请求方法不支持")
	}
	var result Doc
	if r.URL.Path == "/api/session-sources/flow" {
		result = a.store.accessFlow(r.URL.Query())
	} else {
		result = a.store.accessSources(r.URL.Query())
	}
	response(w, r, result, 200, "", nil)
	return true
}
