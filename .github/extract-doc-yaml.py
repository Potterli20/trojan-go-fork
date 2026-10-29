#!/usr/bin/env python3
"""把文档里的 ```yaml 代码块逐段写到指定目录，供工作流逐份喂给真实二进制解析。

为什么需要它：同级的 check-config-keys.py 只比 (父节, 键) 名，不看**值类型**。于是
`log-level: info`（该字段是 int）、`max-idle-timeout: 30s` 这类写法在文档里能长期活着，
只有真的加载一遍才会炸——所以这里只负责把块还原成文件，判据留在工作流那一侧。

DOC_FILES 直接复用审计脚本那份清单，避免两处各维护一份、以后新增文档时只补了一半。
"""

import importlib.util
import os
import re
import sys

FENCE_RE = re.compile(r"```yaml\n(.*?)```", re.S)


def doc_files():
    """取 check-config-keys.py 的 doc_files()（显式清单 + glob 并集）。

    复用同一份清单，避免两处各维护一份、以后新增文档时只补了一半；exec_module 只跑到
    函数定义，不会执行它的 main()。
    """
    here = os.path.dirname(os.path.abspath(__file__))
    spec = importlib.util.spec_from_file_location(
        "check_config_keys", os.path.join(here, "check-config-keys.py"))
    if spec is None or spec.loader is None:
        raise RuntimeError("读不到 check-config-keys.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod.doc_files()


def main():
    if len(sys.argv) != 2:
        print("usage: extract-doc-yaml.py <outdir>", file=sys.stderr)
        return 2
    outdir = sys.argv[1]
    n = 0
    files = 0
    for path in doc_files():
        with open(path, encoding="utf-8") as fh:
            src = fh.read()
        blocks = FENCE_RE.findall(src)
        files += 1
        for i, blk in enumerate(blocks):
            # 段号做前缀，保证文件名唯一且按文档顺序稳定；名字里带上来源便于报错时定位
            name = "%02d_%s_%d.yaml" % (n, re.sub(r"[^\w.-]+", "-", os.path.basename(path)), i)
            with open(os.path.join(outdir, name), "w", encoding="utf-8") as fh:
                fh.write(blk)
            n += 1
    print("从 %d 个文档取出 %d 段 yaml 代码块" % (files, n), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
