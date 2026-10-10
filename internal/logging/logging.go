// Package logging 提供服务器日志的文件输出与大小轮转。
//
// 设计：
//   - RotatingWriter：按大小轮转的追加写入器（超过上限时把当前文件重命名为
//     <path>.1，覆盖旧备份）；并发安全，用于 slog 的文件输出。
//   - TeeHandler：把日志同时写到控制台与文件（文件写失败不影响控制台）。
//   - NewAuditLogger：审计日志（聊天/命令/玩家事件）写入 JSONL 文件；
//     路径为空时返回丢弃型 logger。
package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// RotatingWriter 是按大小轮转的追加写入器。
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
	closed   bool
}

// DefaultMaxBytes 是默认的日志轮转大小（16 MiB）。
const DefaultMaxBytes int64 = 16 << 20

// NewRotatingWriter 打开（或创建）日志文件；maxBytes <= 0 时使用默认值。
func NewRotatingWriter(path string, maxBytes int64) (*RotatingWriter, error) {
	if path == "" {
		return nil, fmt.Errorf("日志文件路径不能为空")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return nil, fmt.Errorf("创建日志目录：%w", err)
	}
	writer := &RotatingWriter{path: path, maxBytes: maxBytes}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

// open 打开文件并记录当前大小。
func (w *RotatingWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("打开日志文件 %s：%w", w.path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("读取日志文件状态 %s：%w", w.path, err)
	}
	w.file = file
	w.size = info.Size()
	return nil
}

// Write 追加写入并在超过大小上限时轮转。
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if w.size+int64(len(p)) > w.maxBytes && w.size > 0 {
		if err := w.rotate(); err != nil {
			// 轮转失败时继续写入当前文件（日志不能拖垮服务器）。
			_ = err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate 把当前文件重命名为 .1（覆盖旧备份）并重新创建。
func (w *RotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	backup := w.path + ".1"
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(w.path, backup); err != nil {
		return err
	}
	return w.open()
}

// Close 关闭文件。
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}

// TeeHandler 把日志同时写到两个 slog.Handler。
type TeeHandler struct {
	console slog.Handler
	file    slog.Handler
}

// NewTeeHandler 返回同时写入 console 与 file 的处理器。
func NewTeeHandler(console, file slog.Handler) *TeeHandler {
	return &TeeHandler{console: console, file: file}
}

// Enabled 报告任一处理器是否启用该级别。
func (h *TeeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.console.Enabled(ctx, level) || h.file.Enabled(ctx, level)
}

// Handle 依次写入两个处理器；返回合并的错误。
func (h *TeeHandler) Handle(ctx context.Context, record slog.Record) error {
	var errs []error
	if h.console.Enabled(ctx, record.Level) {
		errs = append(errs, h.console.Handle(ctx, record.Clone()))
	}
	if h.file.Enabled(ctx, record.Level) {
		errs = append(errs, h.file.Handle(ctx, record.Clone()))
	}
	return errors.Join(errs...)
}

// WithAttrs 返回携带附加属性的 TeeHandler。
func (h *TeeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TeeHandler{console: h.console.WithAttrs(attrs), file: h.file.WithAttrs(attrs)}
}

// WithGroup 返回携带分组名的 TeeHandler。
func (h *TeeHandler) WithGroup(name string) slog.Handler {
	return &TeeHandler{console: h.console.WithGroup(name), file: h.file.WithGroup(name)}
}

// Setup 按参数构建全局日志处理器：
//
//   - level：最低级别（如 "debug"、"info"、"warn"、"error"）；
//   - filePath 非空时，日志同时写入该文件（按 maxBytes 轮转）；
//   - 返回的 io.Closer 用于关闭文件（可安全多次调用）。
func Setup(level string, filePath string, maxBytes int64) (*slog.Logger, io.Closer, error) {
	var slogLevel slog.Level
	if level != "" {
		if err := slogLevel.UnmarshalText([]byte(level)); err != nil {
			return nil, nil, fmt.Errorf("未知日志级别 %q：%w", level, err)
		}
	}
	options := &slog.HandlerOptions{Level: slogLevel}
	console := slog.NewTextHandler(os.Stderr, options)
	if filePath == "" {
		return slog.New(console), io.NopCloser(nil), nil
	}
	writer, err := NewRotatingWriter(filePath, maxBytes)
	if err != nil {
		return nil, nil, err
	}
	file := slog.NewTextHandler(writer, options)
	return slog.New(NewTeeHandler(console, file)), writer, nil
}

// NewAuditLogger 打开审计日志（JSONL 格式，按大小轮转）。
// 路径为空时返回写入 io.Discard 的 logger。
func NewAuditLogger(filePath string, maxBytes int64) (*slog.Logger, io.Closer, error) {
	if filePath == "" {
		return slog.New(slog.NewJSONHandler(io.Discard, nil)), io.NopCloser(nil), nil
	}
	writer, err := NewRotatingWriter(filePath, maxBytes)
	if err != nil {
		return nil, nil, err
	}
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(handler), writer, nil
}
