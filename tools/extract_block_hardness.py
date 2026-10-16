#!/usr/bin/env python3
# gmcs 维护脚本：从官方服务端 jar 的 Blocks 反汇编中机械提取方块硬度
# （destroyTime）与 requiresCorrectToolForDrops，生成
# internal/registry/block_hardness.go。
#
# 需要的外部输入：
#   1. javap 反汇编：javap -p -c -cp <server.jar> <Blocks 混淆类名> > dzs.txt
#      （1.21.11 的 Blocks 混淆类名为 dzs；类文件也可先用 unzip 取出）
#   2. 官方 mappings 文本（server.txt，含 "net.minecraft.world.level.block.Blocks -> dzs:"
#      与逐字段的 "Block NAME -> field" 行）。
#
# 解析规则见 extract()：注册链中的 strength(float,float)/strength(float)/
# instabreak()/destroyTime(float)/requiresCorrectToolForDrops() 给出属性；
# 经 Blocks 内部辅助方法（log/leaves/planks 等）或属性复制
# （ofLegacyCopy/copy/register(String,Block)）构造的方块沿引用链取值；
# 完全没有硬度调用的方块按 Properties 默认 destroyTime = 0 处理（花/空气/火把）。
#
# 用法：
#   python3 tools/extract_block_hardness.py --disasm dzs.txt --mappings server.txt \
#       --version 1.21.11 --out internal/registry/block_hardness.go

import argparse
import re

METHOD_DECL = re.compile(r"^  (.+?)\s([\w$<>]+)\(([^)]*)\);\s*$")
CLINIT_DECL = re.compile(r"^  static \{\};")
FLOAT_LDC = re.compile(r"// float ([-\d.]+)f")
STRING_LDC = re.compile(r"// String (\S+)$")
GETSTATIC = re.compile(r"getstatic\s+#\d+\s+// Field ([\w$.]+):")
INVOKE = re.compile(
    r"invoke(?:virtual|static|special|interface)\s+#\d+\s+// (?:Method|InterfaceMethod) ([\w$.]+):\(([^)]*)\)([\w$/;.]+)"
)

REGISTER_SIGS = {
    "dzs.a:(Ljava/lang/String;Ljava/util/function/Function;Leog$d;)Ldzq;",
    "dzs.a:(Ljava/lang/String;Leog$d;)Ldzq;",
}
COPY_REGISTER_SIGS = {
    "dzs.a:(Ljava/lang/String;Ldzq;)Ldzq;",
    "dzs.b:(Ljava/lang/String;Ldzq;)Ldzq;",
}
PRIMITIVES = {
    "int": "I",
    "long": "J",
    "boolean": "Z",
    "float": "F",
    "double": "D",
    "char": "C",
    "byte": "B",
    "short": "S",
    "void": "V",
}


def decl_args_to_desc(args):
    if not args.strip():
        return ""
    out = []
    for part in args.split(","):
        t = re.sub(r"<.*>", "", part.strip()).replace("...", "[]")
        if t.endswith("[]"):
            out.append("[" + decl_args_to_desc(t[:-2]))
        elif t in PRIMITIVES:
            out.append(PRIMITIVES[t])
        else:
            out.append("L" + t.replace(".", "/") + ";")
    return "".join(out)


def field_map(mappings_path):
    mapping = {}
    inside = False
    with open(mappings_path, encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if line.startswith("net.minecraft.world.level.block.Blocks -> dzs:"):
                inside = True
                continue
            if inside:
                if line.startswith("net.minecraft."):
                    break
                m = re.match(
                    r"\s+net\.minecraft\.world\.level\.block\.Block (\w+) -> (\w+)$",
                    line.rstrip("\n"),
                )
                if m:
                    mapping[m.group(2)] = "minecraft:" + m.group(1).lower()
    return mapping


def split_methods(lines):
    starts = []
    for idx, line in enumerate(lines):
        if CLINIT_DECL.match(line):
            starts.append((idx, "<clinit>", "", line.rstrip("\n")))
            continue
        m = METHOD_DECL.match(line)
        if m and "Code:" in "".join(lines[idx + 1 : idx + 3]):
            starts.append((idx, m.group(2), m.group(3), line.rstrip("\n")))
    out = []
    for pos, (idx, name, args, decl) in enumerate(starts):
        end = starts[pos + 1][0] if pos + 1 < len(starts) else len(lines)
        out.append((name, args, decl, lines[idx:end]))
    return out


def scan_body(body, field_to_block, helper_result, block_results):
    hardness = None
    requires = False
    pending_name = None
    pending_getstatic = None
    floats = []
    results = {}
    unresolved = []

    for raw in body:
        line = raw.rstrip("\n")

        m = STRING_LDC.search(line)
        if m:
            pending_name = "minecraft:" + m.group(1)
            continue

        m = FLOAT_LDC.search(line)
        if m:
            floats.append(float(m.group(1)))
            continue
        if re.search(r"fconst_0\b", line):
            floats.append(0.0)
            continue
        if re.search(r"fconst_1\b", line):
            floats.append(1.0)
            continue
        if re.search(r"fconst_2\b", line):
            floats.append(2.0)
            continue

        m = INVOKE.search(line)
        if m:
            name, args, ret = m.group(1), m.group(2), m.group(3)
            if "." not in name:
                name = "dzs." + name  # javap 对同类方法调用省略类名
            sig = name + ":(" + args + ")" + ret
            if name == "eog$d.a" and args == "FF":
                if len(floats) >= 2:
                    hardness = floats[-2]
                floats = []
            elif name == "eog$d.d" and args == "F":
                if floats:
                    hardness = floats[-1]
                floats = []
            elif name == "eog$d.n" and args == "":
                requires = True
            elif name == "eog$d.d" and args == "":
                hardness = 0.0  # instabreak()
            elif name == "eog$d.e" and args == "F":
                if floats:
                    hardness = floats[-1]  # destroyTime(float)
                floats = []
            elif name in ("eog$d.a", "eog$d.b") and args == "Leog;":
                block = field_to_block.get(pending_getstatic, pending_getstatic)
                if block in block_results and block_results[block][0] is not None:
                    hardness, requires = block_results[block]
                else:
                    hardness, requires = None, False
                    unresolved.append(("copy", pending_name or block))
                floats = []
            elif name.startswith("dzs."):
                if sig in COPY_REGISTER_SIGS:
                    block = field_to_block.get(pending_getstatic, pending_getstatic)
                    if block in block_results and block_results[block][0] is not None:
                        hardness, requires = block_results[block]
                    if pending_name:
                        results[pending_name] = (hardness, requires)
                        if hardness is None:
                            unresolved.append(("copyreg", pending_name, block))
                    pending_name, hardness, requires = None, None, False
                elif sig in REGISTER_SIGS:
                    if pending_name:
                        results[pending_name] = (hardness, requires)
                        if hardness is None:
                            unresolved.append(("chain", pending_name))
                    pending_name, hardness, requires = None, None, False
                elif sig in helper_result:
                    hardness, requires = helper_result[sig]
                floats = []
            continue

        m = GETSTATIC.search(line)
        if m:
            pending_getstatic = m.group(1)
            continue

    return results, unresolved, (hardness, requires)


def extract(disasm_path, mappings_path):
    with open(disasm_path, encoding="utf-8", errors="replace") as fh:
        lines = fh.readlines()
    field_to_block = field_map(mappings_path)
    methods = split_methods(lines)

    helper_result = {}
    helper_bodies = []
    for name, args, decl, body in methods:
        if decl.startswith("  private static eog$d "):
            sig = "dzs." + name + ":(" + decl_args_to_desc(args) + ")Leog$d;"
        elif decl.startswith("  private static dzq "):
            sig = "dzs." + name + ":(" + decl_args_to_desc(args) + ")Ldzq;"
        else:
            continue
        helper_bodies.append((sig, body))
    for _ in range(2):
        for sig, body in helper_bodies:
            _, _, state = scan_body(body, field_to_block, helper_result, {})
            helper_result[sig] = state

    results = {}
    unresolved = []
    for _ in range(8):  # 多轮：复制式注册可能引用文件后面（或链式复制）的方块
        for name, args, decl, body in methods:
            res, unres, _ = scan_body(body, field_to_block, helper_result, results)
            for k, v in res.items():
                prev = results.get(k)
                if prev is not None and prev[0] is not None and v[0] is None:
                    continue
                results[k] = v
            unresolved.extend(unres)

    # 未设置任何硬度调用且非复制来源的方块：Properties 默认 destroyTime = 0。
    chain_names = {entry for kind, *rest in unresolved if kind == "chain" for entry in rest}
    for k, v in results.items():
        if v[0] is None and k in chain_names:
            results[k] = (0.0, v[1])

    still = sorted(k for k, v in results.items() if v[0] is None)
    if still:
        raise SystemExit("未能解析硬度的方块：%s" % ", ".join(still[:20]))
    return results


def fmt(v):
    """把浮点硬度格式化为 Go 字面量（去掉多余的 .0 保持整齐）。"""
    if v == int(v):
        return str(int(v))
    return repr(v)


def emit(version, results, out_path):
    lines = []
    lines.append("//[https://github.com/xhdndmm/gmcs]")
    lines.append("//MIT License")
    lines.append("//Copyright (c) 2026 喜欢电脑的猫咪")
    lines.append("")
    lines.append("package registry")
    lines.append("")
    lines.append("// 方块硬度与“需正确工具才掉落”表（原版 Blocks 静态初始化的机械提取结果）。")
    lines.append("//")
    lines.append(f"// 数据来源：官方 {version} 服务端 jar（net.minecraft.world.level.block.Blocks，")
    lines.append("// 1.21.11 混淆类名 dzs）反汇编——<clinit> 中每个方块注册链里的")
    lines.append("// BlockBehaviour.Properties.strength（destroyTime）、instabreak/destroyTime")
    lines.append("// 与 requiresCorrectToolForDrops()；经辅助方法（log/leaves/planks 等）或")
    lines.append("// 属性复制（ofLegacyCopy/copy、register(String,Block)）构造的方块沿引用链取值；")
    lines.append("// 完全没有硬度调用的方块按 Properties 默认 destroyTime = 0（花/空气/火把）。")
    lines.append(f"// 共 {len(results)} 个方块。")
    lines.append("//")
    lines.append("// 提取步骤（可复现）：")
    lines.append("//   javap -p -c <server.jar 中的 dzs.class> > dzs.txt")
    lines.append("//   提取服务端官方 mappings 中 “Blocks -> dzs:” 段的逐字段映射")
    lines.append("//   python3 tools/extract_block_hardness.py --disasm dzs.txt \\")
    lines.append("//       --mappings server.txt --version " + version + " --out internal/registry/block_hardness.go")
    lines.append("//")
    lines.append("// hardness = destroyTime：-1 表示不可破坏（基岩/末地传送门框架等），")
    lines.append("// 0 表示瞬间破坏；requires 为 requiresCorrectToolForDrops")
    lines.append("// （无正确工具时原版不掉落，见 BlockState.canHarvestBlock 的判定前提）。")
    lines.append("type blockHardness struct {")
    lines.append("\thardness float32")
    lines.append("\trequires bool")
    lines.append("}")
    lines.append("")
    lines.append("var blockHardnessValues = map[string]blockHardness{")
    for name in sorted(results):
        h, req = results[name]
        lines.append('\t"%s": {hardness: %s, requires: %s},' % (name, fmt(h), "true" if req else "false"))
    lines.append("}")
    lines.append("")
    lines.append("// BlockHardness 返回方块的硬度（原版 destroyTime）与是否要求正确工具。")
    lines.append("// 未收录的方块与不可识别状态返回 0, false；-1 表示不可破坏。")
    lines.append("func BlockHardness(state uint16) (hardness float32, requiresCorrectTool bool) {")
    lines.append("\tname, ok := BlockNameByState(state)")
    lines.append("\tif !ok {")
    lines.append("\t\treturn 0, false")
    lines.append("\t}")
    lines.append("\tvalues, ok := blockHardnessValues[name]")
    lines.append("\tif !ok {")
    lines.append("\t\treturn 0, false")
    lines.append("\t}")
    lines.append("\treturn values.hardness, values.requires")
    lines.append("}")
    lines.append("")
    with open(out_path, "w", encoding="utf-8") as fh:
        fh.write("\n".join(lines))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--disasm", required=True, help="Blocks 的 javap -c 输出")
    ap.add_argument("--mappings", required=True, help="官方 mappings 文本（server.txt）")
    ap.add_argument("--version", required=True, help="Minecraft 版本号（写入文件头注释）")
    ap.add_argument("--out", required=True, help="生成的 Go 文件路径")
    args = ap.parse_args()
    results = extract(args.disasm, args.mappings)
    emit(args.version, results, args.out)
    print("已生成 %s（%d 个方块）" % (args.out, len(results)))


if __name__ == "__main__":
    main()
