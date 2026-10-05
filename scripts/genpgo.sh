#!/bin/bash
#
# gmcs PGO 配置生成脚本。
#
# 对热路径包（internal/world、internal/protocol、internal/server）运行 benchmark，
# 采集 CPU profile 并合并写入 cmd/gmcs/default.pgo。构建时 scripts/build.sh 与
# 直接 go build ./cmd/gmcs 都会自动应用该配置（-pgo=auto，见 docs/PERFORMANCE.md）。
#
# 用法：
#   scripts/genpgo.sh                 默认 benchtime 1s
#   scripts/genpgo.sh --benchtime=3s  更长采样（数值更稳，耗时更长）
#   BENCHTIME=3s scripts/genpgo.sh    环境变量方式
#   scripts/genpgo.sh --help          显示帮助
#
# 建议重新生成的时机：
#   - 热路径代码明显变化（区块生成/编码、实体包、服务器 Tick）
#   - 升级 Minecraft/协议版本后
#
# 仅依赖 Go 工具链（含 go tool pprof）。

set -eu

usage() {
	cat <<'EOF'
用法：scripts/genpgo.sh [选项]

选项：
  --benchtime=<时长>  每个 benchmark 的采样时长（默认 1s，如 3s、500ms）
  -h, --help          显示本帮助

环境变量：
  BENCHTIME   同 --benchtime（命令行优先）
EOF
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root_dir=$(dirname -- "$script_dir")
cd "$root_dir"

benchtime=${BENCHTIME:-1s}
for arg in "$@"; do
	case "$arg" in
		--benchtime=*)
			benchtime=${arg#--benchtime=}
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

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/gmcs-pgo.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT

for pkg in world protocol server; do
	printf '==> 采样 internal/%s（benchtime=%s）\n' "$pkg" "$benchtime"
	go test -count=1 -run '^$' -bench=. -benchtime="$benchtime" \
		-cpuprofile="$tmp_dir/$pkg.prof" "./internal/$pkg" >/dev/null
done

printf '==> 合并 profile -> cmd/gmcs/default.pgo\n'
go tool pprof -proto -output=cmd/gmcs/default.pgo \
	"$tmp_dir/world.prof" "$tmp_dir/protocol.prof" "$tmp_dir/server.prof"

printf '==> 完成：%s\n' "$root_dir/cmd/gmcs/default.pgo"
echo "提示：可运行 scripts/test.sh --bench 或按 docs/PERFORMANCE.md 的复现命令对比优化前后数据。"
