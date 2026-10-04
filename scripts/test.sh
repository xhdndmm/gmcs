#!/bin/bash
#
# gmcs 测试脚本：格式检查、构建、vet、单元测试、race 检测与可选 benchmark。
# 仅依赖 POSIX sh 与 Go 工具链，可在 Linux、macOS、BSD、WSL 与 Git Bash 中运行。
#
# 用法：
#   scripts/test.sh              全部检查（格式 + 构建 + vet + 单测 + race）
#   scripts/test.sh --no-race    跳过 race（race 需要 CGO 与受支持的平台）
#   scripts/test.sh --bench      额外运行 benchmark（-bench=. -benchmem）
#   scripts/test.sh --help       显示帮助
#
# 环境变量：
#   TESTFLAGS   附加到单元测试的参数（如 "-run TestChunk -count=5"）
#   RACEFLAGS   附加到 race 测试的参数
#   BENCHFLAGS  附加到 benchmark 的参数
#
# 任一步骤失败即以非零状态退出。

set -eu

usage() {
	cat <<'EOF'
用法：scripts/test.sh [选项]

选项：
  --no-race    跳过 race 检测（用于 race 不可用的平台或环境）
  --bench      额外运行 benchmark
  -h, --help   显示本帮助

示例：
  scripts/test.sh
  scripts/test.sh --no-race
  scripts/test.sh --bench
  TESTFLAGS="-run TestChunk" scripts/test.sh
EOF
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root_dir=$(dirname -- "$script_dir")
cd "$root_dir"

run_race=1
run_bench=0

for arg in "$@"; do
	case "$arg" in
		--no-race)
			run_race=0
			;;
		--bench)
			run_bench=1
			;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			printf '错误：未知选项：%s\n' "$arg" >&2
			usage >&2
			exit 2
			;;
	esac
done

if ! command -v go >/dev/null 2>&1; then
	echo "错误：未找到 go 命令，请先安装 Go 工具链" >&2
	exit 1
fi
if ! command -v gofmt >/dev/null 2>&1; then
	echo "错误：未找到 gofmt 命令" >&2
	exit 1
fi

echo "==> gofmt -l ."
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
	echo "以下文件未通过 gofmt 检查：" >&2
	printf '%s\n' "$unformatted" >&2
	echo "提示：可运行 gofmt -w . 自动修复" >&2
	exit 1
fi

echo "==> go build ./..."
go build ./...

echo "==> go vet ./..."
go vet ./...

echo "==> go test ./..."
# TESTFLAGS 按空白拆分后附加到命令，这是有意为之。
go test -count=1 ${TESTFLAGS:-} ./...

if [ "$run_race" = 1 ]; then
	if [ "$(go env CGO_ENABLED)" = 0 ]; then
		echo "==> 跳过 race 测试：当前环境 CGO_ENABLED=0（race 需要 CGO）"
	else
		echo "==> go test -race ./..."
		if ! go test -race -count=1 ${RACEFLAGS:-} ./...; then
			echo "错误：race 测试失败。" >&2
			echo "如果当前平台不支持 race 检测，请使用 --no-race 跳过。" >&2
			exit 1
		fi
	fi
fi

if [ "$run_bench" = 1 ]; then
	echo "==> go test -bench=. -benchmem ./..."
	go test -count=1 -run '^$' -bench=. -benchmem ${BENCHFLAGS:-} ./...
fi

echo "==> 全部检查通过"
