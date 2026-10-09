#!/bin/sh
# fetch-geo.sh - 取 router 需要的 geosite/geoip 数据（重试 + 尺寸下限 + 多源回退）
#
# 为什么要重试：这些 .dat 只在 GitHub 上，URL 会 302 跳到 raw.githubusercontent.com，
# 那条链路间歇性失败（GNU wget 退出码 4 = Network failure）。以前这里是三条裸 wget，
# 一次抖动就让整个镜像构建失败。
# 为什么要查尺寸：跳转/网关偶尔 200 但内容被截断（或是个 HTML 错误页），只看 wget
# 成败会把"没有数据"的镜像当成好的发出去，router 就此静默失效。
#
# 用法: sh scripts/fetch-geo.sh <输出目录> [文件名...]
#       不给文件名取全部三个；给了只取指定的。GEOTRY/GEOPAUSE 可调重试次数与间隔。
set -u

outdir=${1:-.}
shift 2>/dev/null || true
wanted="$*"
# 三个文件共 7 个候选 URL，最坏耗时 = URL 数 × tries × (wtimeout + pause)
# = 7 × 3 × (15+3) ≈ 6 分钟；再用文件名过滤可以把这一段压到单文件规模。
tries=${GEOTRY:-3}
pause=${GEOPAUSE:-3}
# 单次尝试的网络上限。必须设：GNU wget 默认读超时 900s、自身还会重试 20 次，
# 和这里的循环相乘，一次链路停顿就能把构建拖到十几分钟。-t 1 把重试权收到本脚本，
# -T 给每次尝试定死时限（这两个选项 busybox wget 也支持）。
wtimeout=${GEOTIMEOUT:-15}

# 每行: 目标文件名 最小字节数 候选URL...
entries='geosite.dat 1048576 https://github.com/v2fly/domain-list-community/raw/release/dlc.dat https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat
geoip.dat 204800 https://github.com/Loyalsoldier/geoip/raw/release/geoip.dat https://github.com/v2fly/geoip/raw/release/geoip.dat https://github.com/Loyalsoldier/geoip/releases/latest/download/geoip.dat
geoip-only-cn-private.dat 204800 https://github.com/Loyalsoldier/geoip/raw/release/geoip-only-cn-private.dat https://github.com/v2fly/geoip/raw/release/geoip-only-cn-private.dat'

mkdir -p "$outdir" || exit 1

fail=0
# 用 here-doc 而不是管道：管道会让循环跑在 subshell 里，fail 就传不出来了
while IFS=' ' read -r name min rest; do
	[ -n "${name:-}" ] || continue
	if [ -n "$wanted" ]; then
		hit=0
		for w in $wanted; do
			[ "$w" = "$name" ] && hit=1
		done
		[ "$hit" -eq 1 ] || continue
	fi

	out="$outdir/$name"
	ok=0
	for url in $rest; do
		i=1
		while [ "$i" -le "$tries" ]; do
			if wget -q -T "$wtimeout" -t 1 -O "$out" "$url" >/dev/null 2>&1; then
				size=$(wc -c < "$out" 2>/dev/null || echo 0)
				if [ "$size" -ge "$min" ]; then
					echo "fetched $out <- $url ($size bytes)"
					ok=1
					break
				fi
				echo "$name: $url 只有 $size 字节（下限 $min）" >&2
			fi
			i=$((i + 1))
			[ "$i" -le "$tries" ] && sleep "$pause"
		done
		[ "$ok" -eq 1 ] && break
	done

	if [ "$ok" -ne 1 ]; then
		echo "FAILED $name: 所有候选源都没取到 >= $min 字节" >&2
		fail=1
	fi
done <<EOF
$entries
EOF

if [ "$fail" -ne 0 ]; then
	echo "geo 数据未齐备，终止构建" >&2
	exit 1
fi
