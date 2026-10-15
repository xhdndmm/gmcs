//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package protocol

import "unicode/utf8"

// 网络 NBT 编码所需的最小支持。1.20.2 起网络 NBT 的字符串使用标准 UTF-8
// 与 u16 长度前缀（而非 Java 修改版 UTF-8）。
// 当前实现文本组件的 String Tag 与只含 translate 的 Compound（可翻译组件）。

const (
	nbtTagCompound = 0x0A
	nbtTagString   = 0x08
	nbtTagEnd      = 0x00
)

// AppendNBTString 追加一个只含文本的 NBT String Tag（可用作 Text Component）。
func AppendNBTString(dst []byte, value string) []byte {
	dst = append(dst, nbtTagString)
	return appendNBTStringPayload(dst, value)
}

// AppendNBTTranslatable 追加一个可翻译文本组件（Compound{"translate": key}）。
// 原版踢出消息（如 chat.disabled.chain_broken）即使用该形式。
func AppendNBTTranslatable(dst []byte, key string) []byte {
	dst = append(dst, nbtTagCompound)
	dst = append(dst, nbtTagString)
	dst = appendNBTStringPayload(dst, "translate")
	dst = appendNBTStringPayload(dst, key)
	return append(dst, nbtTagEnd)
}

func appendNBTStringPayload(dst []byte, value string) []byte {
	if len(value) > 0xFFFF {
		value = truncateUTF8(value, 0xFFFF)
	}
	dst = append(dst, byte(len(value)>>8), byte(len(value)))
	return append(dst, value...)
}

// truncateUTF8 将字符串截断到最多 limit 字节，且不切断 UTF-8 编码序列。
func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}
