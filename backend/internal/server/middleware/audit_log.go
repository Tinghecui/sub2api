package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	auditRequestBodyKey       = "ops_request_body" // 与 handler 层 setOpsRequestContext 共用
	defaultMaxCaptureSizeMB   = 16
	bytesPerMB                = 1 << 20
)

// AuditLogMetrics 审计日志监控指标
type AuditLogMetrics struct {
	TotalRequests    uint64 // 总请求数
	TruncatedCount   uint64 // 因超出上限被截断的响应数
	TruncatedBytes   uint64 // 被截断丢弃的总字节数
}

var auditMetrics AuditLogMetrics

// GetAuditLogMetrics 返回当前审计日志监控指标快照
func GetAuditLogMetrics() AuditLogMetrics {
	return AuditLogMetrics{
		TotalRequests:  atomic.LoadUint64(&auditMetrics.TotalRequests),
		TruncatedCount: atomic.LoadUint64(&auditMetrics.TruncatedCount),
		TruncatedBytes: atomic.LoadUint64(&auditMetrics.TruncatedBytes),
	}
}

// cappedBodyWriter wraps gin.ResponseWriter to capture response bytes up to a size limit.
type cappedBodyWriter struct {
	gin.ResponseWriter
	buf       bytes.Buffer
	maxBytes  int64
	written   int64 // total bytes written to upstream (actual response size)
	truncated bool
}

func (w *cappedBodyWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.written += int64(n)
	if !w.truncated {
		remaining := w.maxBytes - int64(w.buf.Len())
		if remaining <= 0 {
			w.truncated = true
		} else if int64(len(b)) <= remaining {
			w.buf.Write(b)
		} else {
			w.buf.Write(b[:remaining])
			w.truncated = true
		}
	}
	return n, err
}

func (w *cappedBodyWriter) WriteString(s string) (int, error) {
	n, err := w.ResponseWriter.WriteString(s)
	w.written += int64(n)
	if !w.truncated {
		remaining := w.maxBytes - int64(w.buf.Len())
		if remaining <= 0 {
			w.truncated = true
		} else if int64(len(s)) <= remaining {
			w.buf.WriteString(s)
		} else {
			w.buf.WriteString(s[:remaining])
			w.truncated = true
		}
	}
	return n, err
}

// AuditLog 审计日志中间件：捕获完整请求/响应并异步上传到对象存储
func AuditLog(sink *service.AuditLogSink, maxCaptureMB int) gin.HandlerFunc {
	if maxCaptureMB <= 0 {
		maxCaptureMB = defaultMaxCaptureSizeMB
	}
	maxCaptureBytes := int64(maxCaptureMB) * bytesPerMB

	return func(c *gin.Context) {
		if sink == nil {
			c.Next()
			return
		}

		startTime := time.Now()
		atomic.AddUint64(&auditMetrics.TotalRequests, 1)

		// 保存请求头快照（在 handler 修改之前）
		reqHeaders := cloneHeaders(c.Request.Header)

		// 包装 ResponseWriter 以捕获响应体（带上限）
		cbw := &cappedBodyWriter{
			ResponseWriter: c.Writer,
			maxBytes:       maxCaptureBytes,
		}
		c.Writer = cbw

		// 执行后续 handler
		c.Next()

		// 构建审计日志条目
		entry := &service.AuditLogEntry{
			Timestamp: startTime,
			Stream:    isStreamResponse(cbw.Header()),
		}

		// request_id
		if rid, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
			entry.RequestID = rid
		}

		// user/api_key metadata（from api key auth middleware）
		if apiKey, ok := GetAPIKeyFromContext(c); ok {
			entry.APIKeyID = apiKey.ID
			if apiKey.User != nil {
				entry.UserID = apiKey.User.ID
			}
		}
		if subject, ok := GetAuthSubjectFromContext(c); ok && entry.UserID == 0 {
			entry.UserID = subject.UserID
		}

		// account/platform/model metadata（handler sets in context.Value）
		if aid, ok := c.Request.Context().Value(ctxkey.AccountID).(int64); ok {
			entry.AccountID = aid
		}
		if p, ok := c.Request.Context().Value(ctxkey.Platform).(string); ok {
			entry.Platform = p
		}
		if m, ok := c.Request.Context().Value(ctxkey.Model).(string); ok {
			entry.Model = m
		}

		// 请求
		entry.RequestHeaders = reqHeaders
		if reqBody, ok := c.Get(auditRequestBodyKey); ok {
			if b, ok := reqBody.([]byte); ok {
				entry.RequestBody = json.RawMessage(b)
			}
		}

		// 响应
		entry.StatusCode = cbw.Status()
		entry.ResponseHeaders = cloneHeaders(cbw.Header())

		// 截断监控
		if cbw.truncated {
			entry.ResponseTruncated = true
			entry.ResponseOriginalSize = cbw.written
			atomic.AddUint64(&auditMetrics.TruncatedCount, 1)
			atomic.AddUint64(&auditMetrics.TruncatedBytes, uint64(cbw.written)-uint64(cbw.buf.Len()))
		}

		captured := cbw.buf.Bytes()
		if len(captured) > 0 {
			if entry.Stream {
				entry.ResponseChunks = splitSSEChunks(captured)
			} else {
				// 使用 string 而非 json.RawMessage，避免非 JSON 响应 marshal 失败
				entry.ResponseBody = string(captured)
			}
		}

		// 时间
		entry.DurationMs = time.Since(startTime).Milliseconds()

		// User-Agent / IP
		entry.UserAgent = c.GetHeader("User-Agent")
		entry.ClientIP = c.ClientIP()

		sink.Submit(entry)
	}
}

// sensitiveHeaders 需要在审计日志中脱敏的请求头（小写匹配）
var sensitiveHeaders = map[string]bool{
	"authorization":   true,
	"x-api-key":       true,
	"x-goog-api-key":  true,
	"cookie":          true,
	"set-cookie":      true,
	"proxy-authorization": true,
}

func cloneHeaders(h http.Header) map[string][]string {
	if h == nil {
		return nil
	}
	clone := make(map[string][]string, len(h))
	for k, v := range h {
		if sensitiveHeaders[strings.ToLower(k)] {
			clone[k] = []string{"[REDACTED]"}
			continue
		}
		vc := make([]string, len(v))
		copy(vc, v)
		clone[k] = vc
	}
	return clone
}

func isStreamResponse(h http.Header) bool {
	ct := h.Get("Content-Type")
	// 匹配 "text/event-stream" 或 "text/event-stream; charset=utf-8" 等
	return strings.HasPrefix(ct, "text/event-stream") || strings.HasPrefix(ct, "application/x-ndjson")
}

func splitSSEChunks(data []byte) []string {
	var chunks []string
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) > 0 {
			chunks = append(chunks, string(line))
		}
	}
	return chunks
}
