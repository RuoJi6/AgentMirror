package lab

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// Never persist raw transport errors: they can contain URLs, credentials or
// provider text. Store only our classification, timing and HTTP status.
func generationTransportError(ctx context.Context, err error, phase string, elapsed, timeout time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	code, detail := "connection_error", "模型连接中断或无法建立，请检查服务是否可用"
	var netErr net.Error
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.As(err, &dnsErr):
		code, detail = "dns_error", "无法解析模型服务域名，请检查模型地址与 DNS"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		code, detail = "connect_error", "无法建立模型服务连接，请检查服务地址与监听状态"
	case errors.As(err, &netErr) && netErr.Timeout():
		code, detail = "request_timeout", fmt.Sprintf("模型响应等待超时（单次请求上限 %s），服务可能仍在推理", timeout)
	case phase == "response_read":
		code, detail = "response_interrupted", "模型响应传输中断，未收到完整内容"
	}
	return &generationResponseError{code: code, retryable: true, detail: detail, diagnostic: Doc{"code": code, "phase": phase, "elapsed_ms": elapsed.Milliseconds(), "timeout_ms": timeout.Milliseconds()}}
}

func generationHTTPError(status int, elapsed time.Duration) error {
	retry := status == 408 || status == 429 || status == 500 || status == 502 || status == 503 || status == 504 || status == 529
	code, detail := "http_error", fmt.Sprintf("模型服务返回 HTTP %d，请检查模型配置及工具调用支持", status)
	if status == 401 || status == 403 {
		code, detail = "authentication_error", fmt.Sprintf("模型服务鉴权失败（HTTP %d），请检查 API Key 和模型访问权限", status)
	} else if status == 429 {
		code, detail = "rate_limited", "模型服务请求受限（HTTP 429），请稍后重试"
	} else if retry {
		code, detail = "upstream_unavailable", fmt.Sprintf("模型服务暂时不可用（HTTP %d）", status)
	}
	return &generationResponseError{code: code, retryable: retry, detail: detail, diagnostic: Doc{"code": code, "http_status": status, "elapsed_ms": elapsed.Milliseconds()}}
}
