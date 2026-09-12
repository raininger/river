#!/usr/bin/env bash
# 编译 river 的各个二进制
#
#   ./build.sh          当前平台 -> bin/monitor, bin/sentinel
#   ./build.sh linux    交叉编译 Linux amd64 -> bin/*-linux-amd64 (部署用)
#   ./build.sh all      以上两个都编
#   ./build.sh clean    删除 bin/
set -euo pipefail

cd "$(dirname "$0")"

# 名称:包路径
BINS=(
	monitor:./cmd/monitor
	sentinel:./cmd/sentinel
)

# 本地编译保留符号，方便调试；部署包剔除符号表并去掉本地路径
build_local() {
	local entry name pkg out
	for entry in "${BINS[@]}"; do
		name=${entry%%:*}
		pkg=${entry#*:}
		out=bin/$name
		echo "编译 $(go env GOOS)/$(go env GOARCH) -> $out"
		go build -o "$out" "$pkg"
	done
}

build_linux() {
	local entry name pkg out
	for entry in "${BINS[@]}"; do
		name=${entry%%:*}
		pkg=${entry#*:}
		out=bin/$name-linux-amd64
		echo "编译 linux/amd64 -> $out"
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
			go build -trimpath -ldflags="-s -w" -o "$out" "$pkg"
	done
}

case "${1:-local}" in
local) build_local ;;
linux) build_linux ;;
all)
	build_local
	build_linux
	;;
clean)
	rm -rf bin
	echo "已清理 bin/"
	;;
*)
	echo "用法: $0 [local|linux|all|clean]" >&2
	exit 2
	;;
esac
