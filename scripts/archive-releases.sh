#!/usr/bin/env bash
# 取回本仓库的历史构建产物：release 资产（zip/tar.gz）与 ghcr.io 上的镜像。
#
# 为什么需要脚本而不是直接翻 Releases 页面：
#   1. GitHub 的 release 列表每页最多 100 条，页面与 API 都按页给——本仓库有 270 个
#      release（V2022.10.17 起），只看第一页就会以为"只有前 100 个包"。
#   2. ghcr.io/potterli20/trojan-go-fork 是**私有包**，匿名 tags/list 与 manifest 一律
#      401，列标签和拉镜像都要带一个有 read:packages 权限的 token。
#   3. master 构建原先只打可变的 snapshot 标签，历史构建会被下一次 push 覆盖掉；
#      docker-build.yml 现在额外打不可变的 sha-<short>，这个脚本按标签或 digest 回捞。
#
# 依赖：gh（已登录，token 需 repo/packages 读权限）；镜像部分还要 skopeo（推荐，能保
# 多架构）或 docker。没有 skopeo 时退回 docker pull + docker save，但那条路**只存本机
# 平台**——本镜像矩阵是 6 个平台，想要全平台必须用 skopeo copy --all。
#
# 用法：
#   scripts/archive-releases.sh                       # 全部 release 的 *.zip + 不碰镜像
#   scripts/archive-releases.sh --assets-pattern '*linux-amd64*'
#   scripts/archive-releases.sh --images              # 顺带归档 ghcr 的全部标签
#   scripts/archive-releases.sh --images --image-tag-filter '^V'   # 只归档 V* 标签
#   scripts/archive-releases.sh --out /srv/mirror --dry-run
#
# 可重复执行：已经存在且非空的文件会被跳过，中断后直接重跑即可续传。
set -euo pipefail

REPO="Potterli20/trojan-go-fork"
OWNER="Potterli20"
PACKAGE="trojan-go-fork"
OUT="${PWD}/archive"
ASSETS_PATTERN='*.zip'
WITH_IMAGES=0
IMAGE_TAG_FILTER='.'
DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) REPO="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --assets-pattern) ASSETS_PATTERN="$2"; shift 2 ;;
    --images) WITH_IMAGES=1; shift ;;
    --image-tag-filter) IMAGE_TAG_FILTER="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h | --help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

need_gh() {
  command -v gh >/dev/null 2>&1 || { echo "缺少 gh（GitHub CLI）" >&2; exit 2; }
  gh auth status >/dev/null 2>&1 || { echo "gh 未登录：先 gh auth login（token 需要 read:packages 才能看私有镜像）" >&2; exit 2; }
}

# 逐资产下载而不是 `gh release download` 整包：那样没法跳过已有文件，中断一次就得重下全部。
download_assets() {
  # --paginate 是关键：不翻页就只会拿到前 100 个 release，正是"看起来只有 100 个包"的由来。
  echo "== release 资产：按 $ASSETS_PATTERN 取，输出 $OUT/releases"
  local rows
  rows=$(gh api -X GET "repos/$REPO/releases" --paginate --per-page 100 |
    jq -r '
      .[] | .tag_name as $tag | .assets[]
      | select(.name | test("(zip|tar\\.gz)$"))
      | [$tag, (.id|tostring), .name, (.size|tostring)] | @tsv')

  # 一次 jq 出全表（270 个 release × 约 40 枚资产），而不是每个 release 重解析一遍整份 JSON。
  local count=0 skipped=0 fetched=0
  while IFS=$'\t' read -r tag asset_id name size; do
    [[ -n "$tag" && -n "$asset_id" ]] || continue
    count=$((count + 1))
    # 这里刻意不引号：ASSETS_PATTERN 是用户给的 glob（'*linux-amd64*'），要的就是模式匹配。
    # shellcheck disable=SC2053
    [[ "$name" == $ASSETS_PATTERN ]] || continue
    local dir="$OUT/releases/$tag"
    if [[ -s "$dir/$name" ]]; then
      skipped=$((skipped + 1))
      continue
    fi
    echo "   取 $tag/$name ($(( ${size:-0} / 1024 )) KiB)"
    if [[ $DRY_RUN -eq 1 ]]; then
      fetched=$((fetched + 1)); continue
    fi
    mkdir -p "$dir"
    # 按资产 id 拉字节流；走 gh 的认证，限速比匿名 curl 宽一个数量级。
    # 先写 .tmp 再改名：中断留下的半个文件不会被下一次运行当成"已存在"而跳过。
    gh api -H "Accept: application/octet-stream" "repos/$REPO/releases/assets/$asset_id" >"$dir/$name.tmp"
    mv "$dir/$name.tmp" "$dir/$name"
    fetched=$((fetched + 1))
  done <<<"$rows"
  echo "   匹配到 $count 枚资产，已存在跳过 $skipped，本次取 $fetched"
}

archive_images() {
  echo "== ghcr 镜像标签：过滤 $IMAGE_TAG_FILTER，输出 $OUT/images"
  local tags_file="$OUT/image-tags.txt"
  mkdir -p "$OUT/images"
  # 私有包这里会 401/403 —— 提示用户换 token 而不是静默给出"没有历史镜像"的错误结论。
  if ! gh api -X GET "users/$OWNER/packages/container/$PACKAGE/versions" \
      --paginate -q '.[].metadata.tags[]?.name' >"$tags_file" 2>/dev/null; then
    echo "   读不到 $OWNER 的 container 包版本：token 缺少 read:packages，或包名/命名空间不对" >&2
    return 1
  fi
  sort -u "$tags_file" -o "$tags_file"
  echo "   共 $(wc -l <"$tags_file") 个标签"

  local tool=""
  if command -v skopeo >/dev/null 2>&1; then
    tool=skopeo
  elif command -v docker >/dev/null 2>&1; then
    tool=docker
    echo "   没有 skopeo：退回 docker，只能存**本机平台**的那一份（镜像矩阵是 6 个平台）" >&2
  else
    echo "   既没有 skopeo 也没有 docker，跳过镜像归档" >&2
    return 1
  fi

  if [[ $tool == skopeo ]]; then
    # 多架构必须 --all：否则归档出来的是单平台 manifest，事后换机器跑不起来还以为是包坏了。
    skopeo login "ghcr.io" -u "$OWNER" --password-stdin <<<"$(gh auth token)" || {
      echo "   skopeo 登录失败" >&2; return 1
    }
  else
    gh auth token | docker login ghcr.io -u "$OWNER" --password-stdin || {
      echo "   docker 登录失败" >&2; return 1
    }
  fi

  while read -r tag; do
    [[ -n "$tag" ]] || continue
    [[ "$tag" =~ $IMAGE_TAG_FILTER ]] || { echo "   按过滤跳过 $tag"; continue; }
    local ref
    ref="ghcr.io/$(echo "$OWNER" | tr '[:upper:]' '[:lower:]')/$PACKAGE:$tag"
    local safe
    safe="${tag//[^A-Za-z0-9._-]/_}"
    if [[ $tool == skopeo ]]; then
      local dst
      dst="$OUT/images/$safe.oci.tar"
      [[ -s "$dst" ]] && { echo "   跳过（已存在）$tag"; continue; }
      echo "   归档 $ref"
      [[ $DRY_RUN -eq 1 ]] && continue
      skopeo copy --all "docker://$ref" "oci-archive:$dst"
    else
      local tar_dst
      tar_dst="$OUT/images/$safe.tar"
      [[ -s "$tar_dst" ]] && { echo "   跳过（已存在）$tag"; continue; }
      echo "   拉取并导出 $ref（仅本机平台）"
      [[ $DRY_RUN -eq 1 ]] && continue
      docker pull "$ref" && docker save -o "$tar_dst" "$ref"
    fi
  done <"$tags_file"
}

main() {
  need_gh
  command -v jq >/dev/null 2>&1 || { echo "缺少 jq" >&2; exit 2; }
  mkdir -p "$OUT"
  [[ $DRY_RUN -eq 1 ]] && echo "*** dry-run：只列不下载 ***"
  download_assets
  if [[ $WITH_IMAGES -eq 1 ]]; then
    # 镜像这一段失败不该把已经完成的资产归档判成整体失败：私有包 401、或本机没有
    # skopeo/docker，都属于"这一半暂时办不了"，把原因说清就够。
    archive_images || echo "   镜像部分未完成（token 缺 read:packages，或本机没有 skopeo/docker）；release 资产已归档" >&2
  fi
  echo "== 完成，产物在 $OUT"
}

main
