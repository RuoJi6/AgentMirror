package lab

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Explicit origins allow domains, NAT ports and TLS proxies without trusting
// arbitrary Host, Forwarded or X-Forwarded-* headers.
type adminAccess struct {
	origins []string
}

func canonicalAdminOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid origin")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(u.Host, "*\\ \t\r\n") || strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("expected an HTTP(S) origin without credentials, path, query or fragment")
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid origin port")
		}
	}
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}

func newAdminAccess(origins []string) (adminAccess, error) {
	access := adminAccess{}
	for _, raw := range origins {
		origin, err := canonicalAdminOrigin(raw)
		if err != nil {
			return access, fmt.Errorf("--admin-origin 无效：%w", err)
		}
		access.origins = append(access.origins, origin)
	}
	return access, nil
}

func (a adminAccess) configured(raw string) bool {
	origin, err := canonicalAdminOrigin(raw)
	if err != nil {
		return false
	}
	for _, allowed := range a.origins {
		if origin == allowed {
			return true
		}
	}
	return false
}

func (a adminAccess) allowsHost(r *http.Request, port int) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if ok && (r.Host == net.JoinHostPort(local.IP.String(), fmtPort(port)) || (local.IP.IsLoopback() && r.Host == "localhost:"+fmtPort(port))) {
		return true
	}
	return a.configured("http://"+r.Host) || a.configured("https://"+r.Host)
}

func (a adminAccess) sameOrigin(r *http.Request, raw string) bool {
	origin, err := canonicalAdminOrigin(raw)
	if err != nil {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	expected, err := canonicalAdminOrigin(scheme + "://" + r.Host)
	if err == nil && origin == expected {
		return true
	}
	// A configured HTTPS origin can terminate TLS at a reverse proxy.
	external, err := canonicalAdminOrigin("https://" + r.Host)
	return err == nil && origin == external && a.configured(external)
}

func (a adminAccess) secure(r *http.Request) bool {
	return r.TLS != nil || a.configured("https://"+r.Host)
}
