package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// AuditLogSinkHealth 审计日志 sink 健康指标
type AuditLogSinkHealth struct {
	QueueDepth     int64  `json:"queue_depth"`
	QueueCapacity  int64  `json:"queue_capacity"`
	DroppedCount   uint64 `json:"dropped_count"`
	WriteFailCount uint64 `json:"write_failed_count"`
	WrittenCount   uint64 `json:"written_count"`
}

// AuditLogSink 异步审计日志上传队列
type AuditLogSink struct {
	store AuditLogStore

	queue chan *AuditLogEntry

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	droppedCount uint64
	writeFailed  uint64
	writtenCount uint64
	prefix       string
}

// NewAuditLogSink 创建审计日志 sink
func NewAuditLogSink(store AuditLogStore, prefix string) *AuditLogSink {
	ctx, cancel := context.WithCancel(context.Background())
	return &AuditLogSink{
		store:  store,
		queue:  make(chan *AuditLogEntry, 5000),
		ctx:    ctx,
		cancel: cancel,
		prefix: prefix,
	}
}

// Start 启动后台上传 goroutine
func (s *AuditLogSink) Start() {
	if s == nil || s.store == nil {
		return
	}
	s.wg.Add(1)
	go s.run()
}

// Stop 停止 sink 并等待剩余日志上传完成
func (s *AuditLogSink) Stop() {
	if s == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
}

// Submit 提交审计日志条目（非阻塞）
func (s *AuditLogSink) Submit(entry *AuditLogEntry) {
	if s == nil || entry == nil {
		return
	}
	select {
	case <-s.ctx.Done():
		return
	default:
	}
	select {
	case s.queue <- entry:
	default:
		atomic.AddUint64(&s.droppedCount, 1)
	}
}

// Health 返回 sink 健康指标
func (s *AuditLogSink) Health() AuditLogSinkHealth {
	if s == nil {
		return AuditLogSinkHealth{}
	}
	return AuditLogSinkHealth{
		QueueDepth:     int64(len(s.queue)),
		QueueCapacity:  int64(cap(s.queue)),
		DroppedCount:   atomic.LoadUint64(&s.droppedCount),
		WriteFailCount: atomic.LoadUint64(&s.writeFailed),
		WrittenCount:   atomic.LoadUint64(&s.writtenCount),
	}
}

func (s *AuditLogSink) run() {
	defer s.wg.Done()
	for {
		select {
		case entry, ok := <-s.queue:
			if !ok {
				return
			}
			s.upload(entry)
		case <-s.ctx.Done():
			// drain remaining
			for {
				select {
				case entry, ok := <-s.queue:
					if !ok {
						return
					}
					s.upload(entry)
				default:
					return
				}
			}
		}
	}
}

func (s *AuditLogSink) upload(entry *AuditLogEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		atomic.AddUint64(&s.writeFailed, 1)
		_, _ = fmt.Fprintf(os.Stderr, "time=%s level=WARN msg=\"audit_log marshal failed\" err=%v\n",
			time.Now().Format(time.RFC3339Nano), err)
		return
	}

	// gzip 压缩
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		atomic.AddUint64(&s.writeFailed, 1)
		return
	}
	if _, err := gz.Write(data); err != nil {
		_ = gz.Close()
		atomic.AddUint64(&s.writeFailed, 1)
		return
	}
	if err := gz.Close(); err != nil {
		atomic.AddUint64(&s.writeFailed, 1)
		return
	}

	// S3 key: {prefix}{date}/{sanitized_request_id}_{random}.json.gz
	// 使用服务端随机后缀防止客户端通过 X-Request-ID 覆写/路径注入
	date := entry.Timestamp.UTC().Format("2006-01-02")
	key := fmt.Sprintf("%s%s/%s_%s.json.gz", s.prefix, date, sanitizeKeySegment(entry.RequestID), randomHex4())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.store.Upload(ctx, key, buf.Bytes()); err != nil {
		atomic.AddUint64(&s.writeFailed, 1)
		_, _ = fmt.Fprintf(os.Stderr, "time=%s level=WARN msg=\"audit_log upload failed\" key=%s err=%v\n",
			time.Now().Format(time.RFC3339Nano), key, err)
		return
	}
	atomic.AddUint64(&s.writtenCount, 1)
}

// sanitizeKeySegment 清理 S3 key 中的路径段，防止客户端注入斜杠或特殊字符
func sanitizeKeySegment(s string) string {
	if s == "" {
		return "unknown"
	}
	// 只保留字母数字和 -_. 其余替换为 _
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	result := b.String()
	if len(result) > 128 {
		result = result[:128]
	}
	return result
}

func randomHex4() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
