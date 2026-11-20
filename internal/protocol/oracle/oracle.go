//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// Package oracle 调用 tools/packetoracle 的官方客户端解码器，校验 gmcs 发出的
// clientbound 包。它只用于测试/开发验证，不进入服务器运行时路径。
//
// 启用条件：设置 GMCS_ORACLE=1，并提供 1.21.11 客户端 jar（GMCS_CLIENT_JAR，
// 默认 ~/.cache/gmcs/versions/1.21.11/client.jar）与官方依赖 jar 目录
// （GMCS_MINECRAFT_LIBS，默认 ~/.cache/gmcs/libraries）。缺少时测试应 Skip。
package oracle

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// ErrUnavailable 表示当前环境没有客户端 jar、Java 或依赖目录。
var ErrUnavailable = errors.New("packet oracle unavailable")

// Case 是一个待校验的包；Packet 必须包含包 ID 与完整负载。
type Case struct {
	Stage  string
	Packet []byte
}

// Result 是官方解码器的结果。
type Result struct {
	OK        bool   `json:"ok"`
	Class     string `json:"class"`
	Error     string `json:"error"`
	Remaining int    `json:"remaining"`
}

type runner struct {
	once    sync.Once
	err     error
	runPath string
	runArgs []string
}

var defaultRunner runner

// Available 报告 oracle 所需文件与工具是否齐全。
func Available() bool {
	_, _, err := prepare()
	return err == nil
}

// CheckBatch 在一次 Java 进程内解码多个包，返回与 cases 等长的结果。
func CheckBatch(cases []Case) ([]Result, error) {
	if len(cases) == 0 {
		return nil, nil
	}
	runPath, runArgs, err := prepare()
	if err != nil {
		return nil, err
	}
	var input bytes.Buffer
	for _, c := range cases {
		switch c.Stage {
		case "play", "configuration":
		default:
			return nil, fmt.Errorf("unknown oracle stage %q", c.Stage)
		}
		input.WriteString(c.Stage)
		input.WriteByte(' ')
		input.WriteString(hex.EncodeToString(c.Packet))
		input.WriteByte('\n')
	}
	cmd := exec.Command(runPath, runArgs...)
	cmd.Stdin = &input
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("packet oracle failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	results := make([]Result, 0, len(cases))
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var result Result
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			return nil, fmt.Errorf("parse oracle output %q: %w", line, err)
		}
		results = append(results, result)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read oracle output: %w", err)
	}
	if len(results) != len(cases) {
		return nil, fmt.Errorf("oracle returned %d results for %d cases (stderr: %s)",
			len(results), len(cases), strings.TrimSpace(stderr.String()))
	}
	return results, nil
}

func prepare() (string, []string, error) {
	defaultRunner.once.Do(func() {
		root, err := repositoryRoot()
		if err != nil {
			defaultRunner.err = err
			return
		}
		clientJar := os.Getenv("GMCS_CLIENT_JAR")
		if clientJar == "" {
			home, _ := os.UserHomeDir()
			clientJar = filepath.Join(home, ".cache", "gmcs", "versions", "1.21.11", "client.jar")
		}
		if info, err := os.Stat(clientJar); err != nil || info.IsDir() {
			defaultRunner.err = fmt.Errorf("%w: client jar %s", ErrUnavailable, clientJar)
			return
		}
		libsDir := os.Getenv("GMCS_MINECRAFT_LIBS")
		if libsDir == "" {
			home, _ := os.UserHomeDir()
			libsDir = filepath.Join(home, ".cache", "gmcs", "libraries")
		}
		libJars, err := collectJars(libsDir)
		if err != nil || len(libJars) == 0 {
			defaultRunner.err = fmt.Errorf("%w: libraries %s: %v", ErrUnavailable, libsDir, err)
			return
		}
		javac, err := exec.LookPath("javac")
		if err != nil {
			defaultRunner.err = fmt.Errorf("%w: javac: %v", ErrUnavailable, err)
			return
		}
		java, err := exec.LookPath("java")
		if err != nil {
			defaultRunner.err = fmt.Errorf("%w: java: %v", ErrUnavailable, err)
			return
		}
		outDir, err := os.MkdirTemp("", "gmcs-packet-oracle-*")
		if err != nil {
			defaultRunner.err = err
			return
		}
		source := filepath.Join(root, "tools", "packetoracle", "ClientOracle.java")
		if _, err := os.Stat(source); err != nil {
			defaultRunner.err = fmt.Errorf("%w: source %s: %v", ErrUnavailable, source, err)
			return
		}
		classpath := strings.Join(append([]string{clientJar}, libJars...), string(os.PathListSeparator))
		compile := exec.Command(javac, "-proc:none", "-cp", classpath, "-d", outDir, source)
		var compileErr bytes.Buffer
		compile.Stderr = &compileErr
		if err := compile.Run(); err != nil {
			defaultRunner.err = fmt.Errorf("compile packet oracle: %w (stderr: %s)", err, strings.TrimSpace(compileErr.String()))
			return
		}
		runClasspath := strings.Join(append([]string{outDir, clientJar}, libJars...), string(os.PathListSeparator))
		defaultRunner.runPath = java
		defaultRunner.runArgs = []string{"-cp", runClasspath, "oracle.ClientOracle"}
	})
	return defaultRunner.runPath, defaultRunner.runArgs, defaultRunner.err
}

func repositoryRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate repository root")
	}
	// file = <root>/internal/protocol/oracle/oracle.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..")), nil
}

func collectJars(dir string) ([]string, error) {
	var jars []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jar") {
			jars = append(jars, path)
		}
		return nil
	})
	return jars, err
}
