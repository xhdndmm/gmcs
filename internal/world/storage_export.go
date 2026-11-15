//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

// EncodeChunkPayload 导出自有区块负载编码，供地图转换工具写入 gmcs 区域文件。
// 返回未压缩负载；调用方需要按 gmcs 区域格式进行 zlib 压缩。
func EncodeChunkPayload(chunk *Chunk) []byte {
	return encodeChunkPayload(chunk)
}

// DecodeChunkPayload 解析自有区块负载（未压缩），供地图转换工具读取。
func DecodeChunkPayload(payload []byte) (*Chunk, error) {
	return decodeChunkPayload(payload)
}

// CompressChunkPayload 压缩自有区块负载，返回 zlib 数据（区域压缩类型 2）。
func CompressChunkPayload(payload []byte) ([]byte, error) {
	return zlibCompress(payload)
}
