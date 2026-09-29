package lab

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func overviewRange(r *http.Request) (int, int) {
	days, offset := 30, 0
	var err error
	if value := r.URL.Query().Get("days"); value != "" {
		days, err = strconv.Atoi(value)
	}
	if err != nil || (days != 7 && days != 30 && days != 90) {
		fail(400, "统计范围支持 7、30、90 天")
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
	}
	if err != nil || offset < -840 || offset > 840 {
		fail(400, "统计时区偏移无效")
	}
	return days, offset
}

func (s *Store) overview(now time.Time, days, offset int) Doc {
	zone := time.FixedZone("dashboard", offset*60)
	local := now.In(zone)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone).AddDate(0, 0, 1-days)
	end := start.AddDate(0, 0, days)
	from, until := start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano)
	daily, indexed := []Doc{}, map[string]Doc{}
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		d := Doc{"date": date, "requests": 0, "deliveries": 0, "reports": 0, "downloads": 0}
		daily, indexed[date] = append(daily, d), d
	}
	modifier := fmt.Sprintf("%+d minutes", offset)
	traffic := Doc{"requests": 0, "deliveries": 0, "reports": 0, "downloads": 0}
	for _, row := range rows(s.db, `SELECT strftime('%Y-%m-%d',at,?) AS date,COUNT(*) AS requests,SUM(CASE WHEN delivered>0 THEN 1 ELSE 0 END) AS deliveries,SUM(CASE WHEN received>0 THEN 1 ELSE 0 END) AS reports,
SUM(CASE WHEN json_type(detail,'$.download')='object' OR json_extract(detail,'$.kind')='file_download' THEN 1 ELSE 0 END) AS downloads
FROM access_requests WHERE julianday(at)>=julianday(?) AND julianday(at)<julianday(?) GROUP BY date`, modifier, from, until) {
		if d := indexed[str(row["date"])]; d != nil {
			for _, k := range []string{"requests", "deliveries", "reports", "downloads"} {
				d[k] = integer(row[k])
				traffic[k] = integer(traffic[k]) + integer(row[k])
			}
		}
	}
	workspaces := Doc{"total": 0, "published": 0, "paused": 0, "draft": 0}
	for _, w := range workspaceListing(s.db) {
		key := str(w["publication_status"])
		workspaces[key] = integer(workspaces[key]) + 1
		workspaces["total"] = integer(workspaces["total"]) + 1
	}
	traffic["sources"] = count(s.db, "SELECT COUNT(DISTINCT NULLIF(ip,'')) FROM access_requests WHERE julianday(at)>=julianday(?) AND julianday(at)<julianday(?)", from, until)
	statuses := Doc{"success": 0, "redirect": 0, "client_error": 0, "server_error": 0, "unknown": 0}
	for _, row := range rows(s.db, `SELECT CASE
WHEN json_extract(detail,'$.status') BETWEEN 200 AND 299 THEN 'success'
WHEN json_extract(detail,'$.status') BETWEEN 300 AND 399 THEN 'redirect'
WHEN json_extract(detail,'$.status') BETWEEN 400 AND 499 THEN 'client_error'
WHEN json_extract(detail,'$.status') BETWEEN 500 AND 599 THEN 'server_error'
ELSE 'unknown' END AS category,COUNT(*) AS n
FROM access_requests WHERE julianday(at)>=julianday(?) AND julianday(at)<julianday(?) GROUP BY category`, from, until) {
		statuses[str(row["category"])] = integer(row["n"])
	}

	recent := array(s.sessions("", "", "", 1)["items"])
	if len(recent) > 5 {
		recent = recent[:5]
	}
	summary := s.stats()
	summary["delivery_events"] = count(s.db, "SELECT COUNT(*) FROM events WHERE kind='delivery'")
	summary["downloads"] = count(s.db, "SELECT COUNT(*) FROM events WHERE kind='file_download'")
	return Doc{"updated_at": now.UTC().Format(time.RFC3339Nano), "days": days, "offset": offset, "daily": daily, "traffic": traffic, "workspaces": workspaces, "statuses": statuses, "summary": summary, "recent": recent}
}
