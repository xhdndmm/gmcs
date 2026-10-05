#!/bin/bash
#
# gmcs 构建脚本。
#
# 默认构建当前平台；也可以一次构建多个目标平台（交叉编译）或全部常用平台。
# 仅依赖 POSIX sh 与 Go 工具链，可在 Linux、macOS、BSD、WSL 与 Git Bash 中运行。
#
# 用法：
#   scripts/build.sh                       构建当前平台
#   scripts/build.sh linux/amd64           构建指定平台
#   scripts/build.sh linux/arm64 windows/amd64
#                                          构建多个指定平台
#   scripts/build.sh --all                 构建全部常用平台
#   scripts/build.sh --all --clean         清理输出目录后重新构建
#   scripts/build.sh --help                显示帮助
#
# 环境变量：
#   OUTPUT_DIR  输出目录（默认 <仓库>/dist）
#   VERSION     产物文件名中的版本标识（默认取 git describe，无 git 时为 dev）
#   STRIP       设为 0 保留调试符号（默认 1，剥离符号与 DWARF）
#   PGO         PGO 配置：auto（默认，使用 cmd/gmcs/default.pgo，缺失时自动忽略）、
#               off 或 profile 文件路径（scripts/genpgo.sh 可重新生成）
#   GOFLAGS     Go 工具链原生识别的附加构建参数（如 -mod=vendor）
#
# 产物命名：gmcs-<version>-<os>-<arch>[.exe]

set -eu

usage() {
	cat <<'EOF'
用法：scripts/build.sh [选项] [os/arch ...]

选项：
  --all        构建全部常用平台（linux/darwin/windows 的 amd64 与 arm64），
               可再追加显式平台参数
  --clean      构建前清理输出目录中的旧产物（仅删除 gmcs-* 文件）
  -h, --help   显示本帮助

示例：
  scripts/build.sh                  构建当前平台
  scripts/build.sh linux/amd64      构建指定平台
  scripts/build.sh --all            交叉编译全部常用平台

环境变量：OUTPUT_DIR、VERSION、STRIP、PGO、GOFLAGS，详见脚本头部注释。
EOF
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root_dir=$(dirname -- "$script_dir")
cd "$root_dir"

build_all=0
clean=0
targets=""

for arg in "$@"; do
	case "$arg" in
		--all)
			build_all=1
			;;
		--clean)
			clean=1
			;;
		-h | --help)
			usage
			exit 0
			;;
		--*)
			printf '错误：未知选项：%s\n' "$arg" >&2
			usage >&2
			exit 2
			;;
		*)
			case "$arg" in
				*/*) ;;
				*)
					printf '错误：平台参数格式应为 os/arch，例如 linux/amd64：%s\n' "$arg" >&2
					exit 2
					;;
			esac
			targets="${targets:+$targets }$arg"
			;;
	esac
done

if ! command -v go >/dev/null 2>&1; then
	echo "错误：未找到 go 命令，请先安装 Go 工具链" >&2
	exit 1
fi

if [ "$build_all" = 1 ]; then
	targets="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64${targets:+ $targets}"
fi

if [ -z "$targets" ]; then
	host_os=$(go env GOOS)
	host_arch=$(go env GOARCH)
	targets="$host_os/$host_arch"
fi

output_dir=${OUTPUT_DIR:-$root_dir/dist}
if [ "$clean" = 1 ]; then
	case "$output_dir" in
		"" | / | . | ..)
			printf '错误：拒绝清理不安全的输出目录：%s\n' "$output_dir" >&2
			exit 1
			;;
	esac
	if [ "$output_dir" = "$root_dir" ]; then
		printf '错误：拒绝清理仓库根目录：%s\n' "$output_dir" >&2
		exit 1
	fi
	rm -f -- "$output_dir"/gmcs-*
fi
mkdir -p -- "$output_dir"

if [ -n "${VERSION:-}" ]; then
	version=$VERSION
elif command -v git >/dev/null 2>&1 && git -C "$root_dir" rev-parse --git-dir >/dev/null 2>&1; then
	version=$(git -C "$root_dir" describe --tags --always --dirty 2>/dev/null || printf 'dev')
else
	version=dev
fi
# 版本标识仅用于文件名，替换可能出现的路径分隔符等不安全字符。
version=$(printf '%s' "$version" | tr '/ ' '__')

count=0
for target in $targets; do
	goos=${target%%/*}
	goarch=${target#*/}
	if [ -z "$goos" ] || [ -z "$goarch" ]; then
		printf '错误：平台参数格式应为 os/arch：%s\n' "$target" >&2
		exit 2
	fi

	suffix=""
	if [ "$goos" = windows ]; then
		suffix=".exe"
	fi
	output="$output_dir/gmcs-$version-$goos-$goarch$suffix"

	printf '==> 编译 %s -> %s\n' "$target" "$output"
	# -buildid= 让构建可复现；-s -w 剥离符号与 DWARF（减小体积）。
	ldflags="-buildid="
	if [ "${STRIP:-1}" = 1 ]; then
		ldflags="-s -w -buildid="
	fi
	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -trimpath -pgo="${PGO:-auto}" -ldflags "$ldflags" -o "$output" ./cmd/gmcs
	count=$((count + 1))
done

printf '==> 完成：共构建 %d 个平台，输出目录 %s\n' "$count" "$output_dir"
