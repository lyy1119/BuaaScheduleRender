#!/usr/bin/env bash
#
# 根据仓库已有 tag 计算下一个版本号，避免手写版本文件。
#
#   .github/scripts/next-version.sh [patch|minor|major]
#
# 只在 stdout 输出裸版本号（不带 v 前缀），日志走 stderr，便于 $(...) 捕获。
# 只认稳定的 vX.Y.Z tag；没有匹配 tag 时从 0.1.0 开始。

set -euo pipefail

bump="${1:-patch}"
case "$bump" in
  patch | minor | major) ;;
  *)
    echo "next-version: bump 必须是 patch/minor/major（收到 '$bump'）" >&2
    exit 2
    ;;
esac

# --sort=-v:refname 按版本排序（v0.10.0 排在 v0.9.0 之前）
latest="$(
  git tag --list 'v[0-9]*' --sort=-v:refname \
    | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
    | head -n 1 || true
)"

if [ -z "$latest" ]; then
  echo "next-version: 没有历史稳定 tag，从 0.1.0 开始" >&2
  echo "0.1.0"
  exit 0
fi

version="${latest#v}"
IFS=. read -r major minor patch <<<"$version"

case "$bump" in
  major)
    major=$((major + 1)); minor=0; patch=0 ;;
  minor)
    minor=$((minor + 1)); patch=0 ;;
  patch)
    patch=$((patch + 1)) ;;
esac

echo "next-version: $latest + $bump -> $major.$minor.$patch" >&2
echo "$major.$minor.$patch"
