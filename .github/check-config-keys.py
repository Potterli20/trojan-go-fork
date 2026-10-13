#!/usr/bin/env python3
"""校验文档与示例配置里的配置键，在**它所在的那一节**里真的被声明过。

为什么不是"全局键集合"比对：Go 的 encoding/json 静默忽略未知键，而真实发生过的坏法都是
**放错节**的键——`quic.enabled`、`ssl.verify-cert`、`ssl.insecure`、`api.user`。这些键名在
别的节里完全合法（`enabled` 到处都有），所以拿全仓键名做集合比对时，注入 `quic.enabled: true`
照样全绿（2026-10-13 实测）。因此本脚本按 (父节, 键) 成对校验。

规则来源：config.WithJSONConfig/WithYAMLConfig 把**同一份文档**喂给每个已注册模块
（见 config/AGENTS.md），各模块结构体里没声明的键被静默丢弃。
YAML 与 JSON 用的是同一批结构体，但两套标签拼法不同（`json:"local_addr" yaml:"local-addr"`），
所以两种拼法都算合法。

三道自证（缺一即这道门不可信）：
  * 分母下限：根键数、节数、比对过的键对数都要达到下限，防止提取器坏掉时
    "零未知键"因为什么都没检查而虚假通过。（本脚本开发时就靠它抓到过一次
    "字符类误排除字母 s、把所有键名在 s 处截断"的解析器 bug。）
  * 阳性对照：注入一枚必然不存在的根层键，必须变红。
  * 真实回归对照：`quic.enabled` 必须变红（它 2026-10-11 已被删除，而旧文档还在教）。

退出码：0 通过；1 失败。
"""

import glob
import json
import os
import re
import sys

DOC_FILES = [
    "README.md",
    "docs/quic_usage.md",
    "docs/configuration.md",
    "config/README_cloudflare.md",
]

# 不是配置键的噪声：HTTP 头名、表格里的类型列、取值示例等
ALLOWED_NOISE = {
    "User-Agent", "Accept-Language", "Host", "Connection", "Upgrade",
    "string", "int", "bool", "uint64", "[]string", "object", "map",
    "proxy", "bypass", "block", "bbr", "brutal", "force-brutal", "reno",
    "cubic", "as_is", "as-is", "asis", "ip_if_non_match", "ip-if-non-match",
    "ipondemand", "client", "server", "forward", "nat", "custom",
    "linux", "windows", "darwin", "amd64", "arm64", "memory", "sqlite", "mysql",
}

# custom 运行类型里节点 config 块的父级：它会被当成**一份独立文档**喂给模块，
# 所以块内的键按根层规则校验（proxy/custom/custom.go 只 marshal 节点自己的 config）
ROOT_SCOPE_PARENTS = {"config"}

# 取自 2026-10-13 实测：struct=45 / 根键=34（含两套拼法约 55）/ 节=13+，留三成余量
MIN_ROOT_KEYS = 40
MIN_SECTIONS = 14
MIN_PAIRS_SCANNED = 150

TAG_RE = re.compile(r'(json|yaml):"([^",\s]+)')


def struct_defs(src):
    """返回 {结构体名: [(所有标签拼法, Go类型名)]}，同文件内的导出 struct 都收。"""
    out = {}
    for m in re.finditer(r"type\s+([A-Z]\w*)\s+struct\s*\{(.*?)\n\}", src, re.S):
        name, body = m.group(1), m.group(2)
        fields = []
        for line in body.split("\n"):
            line = line.split("//")[0].strip()
            if not line:
                continue
            fm = re.match(r"[A-Z]\w*\s+([\w\*\[\]\.]+)\s*`", line)
            if not fm:
                continue
            gotype = fm.group(1)
            tags = {v for _, v in TAG_RE.findall(line)}
            tags.discard("-")
            for t in tags:
                fields.append((t, gotype))
        if fields:
            out[name] = fields
    return out


def build_schema():
    """产出 (根键集合, 节名->子键集合)。递归展开同包内的嵌套 struct。"""
    root_keys = set()
    children = {}
    structs_seen = 0
    for path in glob.glob("**/*.go", recursive=True):
        if path.endswith("_test.go"):
            continue
        with open(path, encoding="utf-8", errors="replace") as fh:
            src = fh.read()
        if "RegisterConfigCreator" not in src and "type Config struct" not in src:
            continue
        defs = struct_defs(src)
        if not defs:
            continue
        structs_seen += len(defs)

        def register(section, struct_name, depth=0):
            """把 struct_name 的字段登记成 section 的子键；嵌套 struct 递归。"""
            kids = defs.get(struct_name)
            if not kids or depth > 4:
                return
            bucket = children.setdefault(section, set())
            for key, gotype in kids:
                bucket.add(key)
                clean = gotype.lstrip("*")
                if clean.startswith("[]"):
                    clean = clean[2:]
                clean = clean.split("[")[0]
                if clean in defs and clean != struct_name:
                    register(key, clean, depth + 1)

        if "Config" in defs:
            register("__root__", "Config")
            root_keys.update(children["__root__"])
            # 同包里其它被 root 引用到的 struct 已由 register 递归处理
    children.pop("__root__", None)
    return root_keys, children, structs_seen


def pairs_from_yaml(block):
    """按缩进跟踪当前父节，产出 (父节或 None, 键)。正确处理列表项。"""
    pairs = []
    stack = []            # [(缩进, 节名)]
    for raw in block.split("\n"):
        line = raw.split("#")[0].split("//")[0]
        if not line.strip():
            continue
        stripped = line.strip()
        dash = stripped.startswith("- ")
        content = stripped[2:] if dash else stripped
        # 列表项的缩进按它承载的键来算（"- node:" 与 "node:" 同层）
        base = len(line) - len(line.lstrip())
        indent = base + (2 if dash else 0)
        m = re.match(r"([A-Za-z_][A-Za-z0-9_-]*):(.*)$", content)
        if not m:
            continue
        key, rest = m.group(1), m.group(2).strip()
        while stack and stack[-1][0] >= indent:
            stack.pop()
        parent = stack[-1][1] if stack else None
        pairs.append((parent, key))
        for fm in re.finditer(r"[{,]\s*([A-Za-z_][A-Za-z0-9_-]*):", rest):
            pairs.append((key, fm.group(1)))
        if rest == "" or rest[0] in "|>" or "{" in rest:
            stack.append((indent, key))
    return pairs


def pairs_from_obj(obj, parent=None):
    pairs = []
    if isinstance(obj, dict):
        for k, v in obj.items():
            if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_-]*", str(k)):
                continue
            pairs.append((parent, k))
            if isinstance(v, (dict, list)):
                pairs += pairs_from_obj(v, k)
    elif isinstance(obj, list):
        for v in obj:
            pairs += pairs_from_obj(v, parent)
    return pairs


def strip_json_comments(txt):
    """文档里的 JSON 片段常带 // 说明；剥掉字符串外的 // 注释再解析。"""
    out = []
    for line in txt.split("\n"):
        res = []
        inq = False
        esc = False
        for i, c in enumerate(line):
            if esc:
                esc = False
                res.append(c)
                continue
            if c == "\\":
                esc = True
                res.append(c)
                continue
            if c == '"':
                inq = not inq
                res.append(c)
                continue
            if not inq and c == "/" and line[i + 1:i + 2] == "/":
                break
            res.append(c)
        out.append("".join(res))
    return "\n".join(out)


def pairs_from_json(block):
    txt = strip_json_comments(block.strip())
    try:
        if not txt.startswith("{"):
            txt = "{" + txt + "}"
        return pairs_from_obj(json.loads(txt))
    except Exception:
        keys = set(re.findall(r'"([A-Za-z_][A-Za-z0-9_-]*)"\s*:', txt))
        return [(None, k) for k in keys]


def extract(path):
    with open(path, encoding="utf-8") as fh:
        src = fh.read()
    if path.endswith(".md"):
        pairs = []
        for b in re.findall(r"```yaml\n(.*?)```", src, re.S):
            pairs += pairs_from_yaml(b)
        for b in re.findall(r"```json\n(.*?)```", src, re.S):
            pairs += pairs_from_json(b)
        return pairs
    return pairs_from_yaml(src)


def evaluate(pairs, root_keys, children):
    bad = []
    for parent, key in pairs:
        if key in ALLOWED_NOISE:
            continue
        if parent is None or parent in ROOT_SCOPE_PARENTS:
            if key not in root_keys:
                bad.append(("(根层)" if parent is None else parent, key))
        else:
            kids = children.get(parent)
            if kids is None:
                continue          # 父级不是已知节（如 headers 这类自由 map）
            if key not in kids:
                bad.append((parent, key))
    return bad


def main():
    print("== 配置键审计（按父节作用域）==")
    root_keys, children, structs = build_schema()
    print("代码侧：struct=%d  根键=%d  节=%d" % (structs, len(root_keys), len(children)))
    if len(root_keys) < MIN_ROOT_KEYS:
        print("[FAIL] 根键只提取到 %d（下限 %d）—— 解析器可能已失效" % (len(root_keys), MIN_ROOT_KEYS))
        return 1
    if len(children) < MIN_SECTIONS:
        print("[FAIL] 节只提取到 %d（下限 %d）—— 嵌套解析可能已失效" % (len(children), MIN_SECTIONS))
        return 1

    targets = list(DOC_FILES) + sorted(glob.glob("config/*.yaml"))
    missing = [t for t in targets if not os.path.exists(t)]
    if missing:
        print("[FAIL] 待检文件缺失: %s" % missing)
        return 1

    total_pairs = 0
    all_bad = {}
    for t in targets:
        pairs = extract(t)
        total_pairs += len(pairs)
        bad = sorted(set(evaluate(pairs, root_keys, children)))
        if bad:
            all_bad[t] = bad
            print("  [FAIL] %-34s 键对=%3d 非法=%d -> %s" % (t, len(pairs), len(bad), bad))
        else:
            print("  [ok]   %-34s 键对=%3d" % (t, len(pairs)))

    if total_pairs < MIN_PAIRS_SCANNED:
        print("[FAIL] 总共只比对 %d 枚键对（下限 %d）—— 提取器很可能已失效"
              % (total_pairs, MIN_PAIRS_SCANNED))
        return 1
    print("比对过的 (节,键) 对总数: %d" % total_pairs)

    if evaluate([(None, "zz-probe-root-key")], root_keys, children) == []:
        print("[FAIL] 阳性对照（根层假键）没有变红 —— 判据失效")
        return 1
    if "quic" not in children:
        print("[FAIL] 没解析出 quic 节，无法做真实回归对照")
        return 1
    if evaluate([("quic", "enabled")], root_keys, children) == []:
        print("[FAIL] `quic.enabled` 未被判为非法 —— 这道门挡不住本轮修掉的那类 bug")
        return 1
    print("[ok]   两枚阳性对照均按预期变红（含 `quic.enabled` 真实回归对照）")

    if all_bad:
        print("\n[FAIL] %d 个文件存在放错节/未声明的键" % len(all_bad))
        return 1
    print("\n[PASS] 文档与示例配置的每枚键都在其所属节里被声明过")
    return 0


if __name__ == "__main__":
    sys.exit(main())
