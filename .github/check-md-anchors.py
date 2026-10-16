#!/usr/bin/env python3
"""校验 Markdown 里的站内锚点是否真的指向存在的标题。

GitHub 给标题生成 id 的规则很简单也很无情：小写、丢掉标点/符号/控制字符
（下划线和连字符保留）、空格换成连字符、重名的往后追加 -1/-2。
所以 "### CDN 中转配置" 的锚点是 #cdn-中转配置，写成 "#cdn 中转配置" 就是死链——
浏览器会把空格编成 %20，和任何一个 id 都对不上。

这类错误不会让任何测试变红，页面也不会报错，只是目录点下去不动，
可以活很久。所以在这里立个门。

用法：python3 .github/check-md-anchors.py [文件...]
不传参数时检查 README.md / *.md / docs/**.md / config/**.md。
"""

from __future__ import annotations

import difflib
import glob
import os
import re
import sys
import unicodedata

# 标题行：ATX 形式，允许闭合的 #
HEADING_RE = re.compile(r"^(#{1,6})\s+(.*?)\s*#*\s*$")
# 站内链接：[text](...#anchor)。这里必须连空格一起收进来——"#cdn 中转配置"
# 这种带空格的写法正是最常见的死锚点，按 \S 截断会把它整个漏掉。
LINK_RE = re.compile(r"\[[^\]]*\]\(([^)\s][^)]*)\)")
EXTERNAL_RE = re.compile(r"^[a-zA-Z][a-zA-Z0-9+.\-]*:")
FENCE_RE = re.compile(r"^\s*(```|~~~)")


def slugify(title: str) -> str:
    """按 github-slugger 的规则把标题转成 id。"""
    title = unicodedata.normalize("NFC", title.strip()).lower()
    kept = []
    for ch in title:
        if ch == " ":
            kept.append(ch)
            continue
        if ch in "-_":
            kept.append(ch)
            continue
        # P 标点 / S 符号 / M 组合记号 / C 控制与格式字符都不进 id
        if unicodedata.category(ch)[0] in "PSMC":
            continue
        kept.append(ch)
    return "".join(kept).strip().replace(" ", "-")


def anchors_of(path: str) -> set[str]:
    """文件里所有可被指向的 id（含重名时的 -1/-2 变体）。"""
    ids: set[str] = set()
    seen: dict[str, int] = {}
    in_fence = False
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            if FENCE_RE.match(line):
                in_fence = not in_fence
                continue
            if in_fence:
                continue
            match = HEADING_RE.match(line)
            if not match:
                continue
            base = slugify(match.group(2))
            if not base:
                continue
            # github-slugger：同名第二次起追加 -1、-2……
            count = seen.get(base, 0)
            seen[base] = count + 1
            ids.add(base if count == 0 else f"{base}-{count}")
    return ids


def check(path: str) -> list[str]:
    problems = []
    local = anchors_of(path)
    directory = os.path.dirname(path)
    cached: dict[str, set[str]] = {}
    with open(path, encoding="utf-8") as handle:
        text = handle.read()

    # 链接写成 [文字]#锚点（括号丢了）连链接都算不上，本门一条都不会报——
    # 但它比死锚点更糟：目录整行变成裸文本。所以先单独盯一眼这种畸形写法。
    in_fence = False
    for number, line in enumerate(text.splitlines(), 1):
        if FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        for stray in re.findall(r"\]\[?#[^\s\]]+", line):
            if not stray.startswith("]["):  # 正常引用式链接 [文字][引用名] 另说
                problems.append(f"{path}:{number}: 链接括号丢了（{stray}），这行不会被渲染成链接")

    for url in LINK_RE.findall(text):
        url = url.strip()
        if EXTERNAL_RE.match(url) or url.startswith("//"):
            continue  # 外链、协议相对链接都不归本门管
        target, _, anchor = url.partition("#")
        if not anchor:
            continue
        target = target.strip()
        if target.startswith("/"):
            continue  # 站内绝对路径只指向站点根，本地无从校验标题
        if target:
            # 指向别的路径：#L12 之类是 GitHub 源码行号锚，不归标题管
            if not target.endswith((".md", ".markdown")):
                continue
            resolved = os.path.normpath(os.path.join(directory, target))
            if not os.path.isfile(resolved):
                problems.append(f"{path}: 链接指向不存在的文件 {target}#{anchor}")
                continue
            if resolved not in cached:
                cached[resolved] = anchors_of(resolved)
            pool = cached[resolved]
        else:
            pool = local
        if anchor not in pool:
            hint = closest(anchor, pool)
            problems.append(f"{path}: 死锚点 #{anchor}{hint}")
    return problems


def closest(anchor: str, pool: set[str]) -> str:
    """给个最接近的正确写法，改起来不用手算 slug。

    用相似度而不是公共前缀：标题带序号（"3. URL 模式（客户端）"）时，
    第一个字符就对不上，前缀匹配会一路退化成"没有相近锚点"。
    """
    if not pool:
        return "（该文件里没有可指向的标题）"
    want = anchor.replace("-", "").replace(" ", "").replace("%20", "")
    best, best_ratio = None, 0.0
    for candidate in pool:
        got = candidate.replace("-", "").replace("_", "")
        ratio = difflib.SequenceMatcher(None, want, got).ratio()
        if ratio > best_ratio:
            best, best_ratio = candidate, ratio
    if not best or best_ratio < 0.34:
        return "（没有任何相近锚点）"
    return f"（最接近的现存锚点：#{best}）"


def main(argv: list[str]) -> int:
    explicit = argv[1:]
    if explicit:
        candidates = explicit
    else:
        candidates = (
            ["README.md"]
            + glob.glob("*.md")
            + glob.glob("docs/**/*.md", recursive=True)
            + glob.glob("config/**/*.md", recursive=True)
        )
    targets = sorted({p for p in candidates if os.path.isfile(p)})

    # 分母自证：目录改名或 glob 失配时，"一个文件都没扫到"绝不能读成通过。
    floor = int(os.environ.get("MIN_MD_FILES", 6 if not explicit else 1))
    if len(targets) < floor:
        print(f"[FAIL] 只扫到 {len(targets)} 个 Markdown 文件（下限 {floor}）—— 目录结构或匹配规则变了")
        return 1

    problems: list[str] = []
    for path in targets:
        problems.extend(check(path))
    if problems:
        for problem in problems:
            print(f"[FAIL] {problem}")
        print(f"共 {len(problems)} 个锚点问题（死锚点或畸形链接，已检查 {len(targets)} 个文件）")
        return 1
    print(f"[PASS] {len(targets)} 个 Markdown 文件的站内锚点都能落到真实标题上，链接写法也完整")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
