package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotatingWriterRotation 验证超过大小上限时轮转到 .1 备份并继续写入。
func TestRotatingWriterRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gmcs.log")
	writer, err := NewRotatingWriter(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	// 第一次写入不超过上限。
	if _, err := writer.Write([]byte("first line\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatal("backup created before the size limit was reached")
	}
	// 第二次写入超过上限：原文件轮转到 .1。
	if _, err := writer.Write([]byte(strings.Repeat("x", 80) + "\n")); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("backup missing after rotation: %v", err)
	}
	if string(backup) != "first line\n" {
		t.Fatalf("backup content = %q, want %q", backup, "first line\n")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(current) == 0 || string(current) != strings.Repeat("x", 80)+"\n" {
		t.Fatalf("current log content unexpected: %q", current)
	}

	// 再次超过上限：旧备份被覆盖。
	if _, err := writer.Write([]byte(strings.Repeat("y", 80) + "\n")); err != nil {
		t.Fatal(err)
	}
	backup, err = os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(backup), strings.Repeat("x", 80)) {
		t.Fatalf("second rotation did not replace the backup: %q", backup)
	}
}

// TestRotatingWriterCloseAndReopen 验证关闭后写入报错、重新打开可追加。
func TestRotatingWriterCloseAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gmcs.log")
	writer, err := NewRotatingWriter(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("line\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("after close\n")); err == nil {
		t.Fatal("write after Close should fail")
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("second Close should be a no-op, got %v", err)
	}

	reopened, err := NewRotatingWriter(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Write([]byte("appended\n")); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "line\nappended\n" {
		t.Fatalf("reopened content = %q", content)
	}
}

// TestSetupWritesFileAndFiltersLevel 验证 Setup 同时写文件并遵守级别过滤。
func TestSetupWritesFileAndFiltersLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "server.log")
	logger, closer, err := Setup("warn", path, 0)
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message", "key", "value")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Contains(text, "debug message") || strings.Contains(text, "info message") {
		t.Fatalf("level filter not applied: %q", text)
	}
	if !strings.Contains(text, "warn message") || !strings.Contains(text, "key=value") {
		t.Fatalf("warn message missing from log file: %q", text)
	}
}

// TestSetupInvalidLevel 验证非法级别返回错误。
func TestSetupInvalidLevel(t *testing.T) {
	if _, _, err := Setup("notalevel", filepath.Join(t.TempDir(), "x.log"), 0); err == nil {
		t.Fatal("Setup with an invalid level should fail")
	}
	// 未指定级别时默认 INFO。
	logger, closer, err := Setup("", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	if logger == nil {
		t.Fatal("Setup returned a nil logger")
	}
	if !logger.Handler().Enabled(t.Context(), slog.LevelInfo) {
		t.Fatal("default level should enable INFO")
	}
	if logger.Handler().Enabled(t.Context(), slog.LevelDebug) {
		t.Fatal("default level should not enable DEBUG")
	}
}

// TestNewAuditLoggerJSONL 验证审计日志以 JSONL 写入并可解析。
func TestNewAuditLoggerJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, closer, err := NewAuditLogger(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("command", "player", "Alice", "command", "/give stone")
	logger.Info("chat", "player", "Alice", "message", "hello")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("audit lines = %d, want 2 (%q)", len(lines), content)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, "\"msg\"") {
			t.Fatalf("audit line is not JSONL: %q", line)
		}
	}
	if !strings.Contains(lines[0], "\"player\":\"Alice\"") {
		t.Fatalf("audit attributes missing: %q", lines[0])
	}
	if !strings.Contains(lines[0], "command") || !strings.Contains(lines[1], "chat") {
		t.Fatalf("audit message types unexpected: %q", content)
	}
}

// TestNewAuditLoggerEmptyPath 验证空路径返回丢弃型 logger（不创建文件）。
func TestNewAuditLoggerEmptyPath(t *testing.T) {
	logger, closer, err := NewAuditLogger("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	// 丢弃型 logger 不应 panic；信息写入被静默忽略。
	logger.Info("ignored", "key", fmt.Sprint(1))
}

// TestRotatingWriterEmptyPath 验证空路径报错。
func TestRotatingWriterEmptyPath(t *testing.T) {
	if _, err := NewRotatingWriter("", 0); err == nil {
		t.Fatal("empty path should fail")
	}
}
