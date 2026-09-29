package lab

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Doc preserves the existing SQLite JSON contract, including custom report fields.
type Doc = map[string]any

type problem struct {
	message string
	status  int
}

func (e problem) Error() string       { return e.message }
func fail(status int, message string) { panic(problem{message, status}) }
func check(err error) {
	if err != nil {
		panic(err)
	}
}

// A request/transaction boundary converts validation failures into HTTP errors.
// All transactions defer Rollback before invoking any validating helper.
func caught(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return fmt.Errorf("unexpected failure: %v", value)
}
func decode(raw string) Doc {
	var value Doc
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	check(decoder.Decode(&value))
	return value
}
func jsonBytes(value any) []byte {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	check(encoder.Encode(value))
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}
func dump(value any) string { return string(jsonBytes(value)) }
func pretty(value any) string {
	var b bytes.Buffer
	check(json.Indent(&b, jsonBytes(value), "", "  "))
	return b.String()
}
func clone(value Doc) Doc { return decode(dump(value)) }
func object(value any) Doc {
	if d, ok := value.(map[string]any); ok {
		return d
	}
	return nil
}
func array(value any) []any {
	if a, ok := value.([]any); ok {
		return a
	}
	if docs, ok := value.([]Doc); ok {
		result := make([]any, len(docs))
		for i, d := range docs {
			result[i] = d
		}
		return result
	}
	return nil
}
func str(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
func boolean(value any) bool { b, _ := value.(bool); return b }
func number(value any) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, e := n.Int64()
		return i, e == nil
	}
	return 0, false
}
func integer(value any) int { n, _ := number(value); return int(n) }
func fallback(value any, defaultValue any) any {
	if value == nil {
		return defaultValue
	}
	return value
}
func optional(doc Doc, key string, defaultValue any) any {
	if value, exists := doc[key]; exists {
		return value
	}
	return defaultValue
}
func has(values any, value string) bool {
	for _, v := range array(values) {
		if str(v) == value {
			return true
		}
	}
	return false
}
func merge(target Doc, source Doc) Doc {
	for k, v := range source {
		target[k] = v
	}
	return target
}
func timestamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000+00:00") }
func randomHex(size int) string {
	b := make([]byte, size)
	_, err := rand.Read(b)
	check(err)
	return hex.EncodeToString(b)
}
func randomToken(size int) string {
	b := make([]byte, size)
	_, err := rand.Read(b)
	check(err)
	return base64.RawURLEncoding.EncodeToString(b)
}
func textField(doc Doc, key string, limit int, required bool) string {
	raw := optional(doc, key, "")
	value, ok := raw.(string)
	if !ok || utf8.RuneCountInString(value) > limit || (required && strings.TrimSpace(value) == "") {
		fail(400, fmt.Sprintf("%s 必须是非空文本，长度不超过 %d", key, limit))
	}
	if key == "body" {
		return value
	}
	return strings.TrimSpace(value)
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var fieldPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var variablePattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

func publicURL(value string) string {
	u, err := url.Parse(value)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		fail(400, "测试地址需为 http(s)://主机:端口，不包含路径或账号")
	}
	if u.Port() != "" {
		var n int
		_, e := fmt.Sscanf(u.Port(), "%d", &n)
		if e != nil || n < 1 || n > 65535 {
			fail(400, "端口格式不正确")
		}
	}
	return strings.TrimRight(value, "/")
}

type queryer interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func exec(q queryer, query string, args ...any) sql.Result {
	result, err := q.Exec(query, args...)
	check(err)
	return result
}
func rows(q queryer, query string, args ...any) []Doc {
	rs, err := q.Query(query, args...)
	check(err)
	defer rs.Close()
	columns, err := rs.Columns()
	check(err)
	result := []Doc{}
	for rs.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		check(rs.Scan(ptrs...))
		item := Doc{}
		for i, key := range columns {
			if b, ok := values[i].([]byte); ok {
				item[key] = string(b)
			} else {
				item[key] = values[i]
			}
		}
		result = append(result, item)
	}
	check(rs.Err())
	return result
}
func count(q queryer, query string, args ...any) int {
	var n int
	check(q.QueryRow(query, args...).Scan(&n))
	return n
}
