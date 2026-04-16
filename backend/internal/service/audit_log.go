package service

import (
	"context"
	"encoding/json"
	"time"
)

// AuditLogEntry 完整审计日志条目，保存到对象存储
type AuditLogEntry struct {
	RequestID string    `json:"request_id"`
	Timestamp time.Time `json:"timestamp"`
	UserID    int64     `json:"user_id"`
	APIKeyID  int64     `json:"api_key_id"`
	AccountID int64     `json:"account_id"`
	Platform  string    `json:"platform"`
	Model     string    `json:"model"`
	Stream    bool      `json:"stream"`

	// 请求
	RequestHeaders map[string][]string `json:"request_headers"`
	RequestBody    json.RawMessage     `json:"request_body"`

	// 响应（使用 string 而非 json.RawMessage，因为流式响应或错误页面可能不是有效 JSON）
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	ResponseBody    string              `json:"response_body,omitempty"`   // 非流式完整响应（原始字节 base64 or utf-8）
	ResponseChunks  []string            `json:"response_chunks,omitempty"` // 流式 SSE chunks

	// 截断标记
	ResponseTruncated    bool  `json:"response_truncated,omitempty"`     // 响应是否因超出上限被截断
	ResponseOriginalSize int64 `json:"response_original_size,omitempty"` // 截断前的原始大小（字节）

	// 元数据
	StatusCode   int    `json:"status_code"`
	DurationMs   int64  `json:"duration_ms"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	UserAgent    string `json:"user_agent"`
	ClientIP     string `json:"client_ip"`
}

// AuditLogStore 审计日志对象存储接口
type AuditLogStore interface {
	Upload(ctx context.Context, key string, data []byte) error
}
