package world

// 本文件实现 section 方块状态的紧凑存储：调色板 + 紧密位流。
//
// 旧的“每个方块 2 字节数组”实现对一个 16³ section 恒定分配 8 KiB；
// 而地形区块的 section 通常只包含 1–4 种方块（石头、泥土、水、空气），
// 用调色板索引（1–2 位/方块，512–1024 字节）即可，内存降低 8–16 倍。
// 该表示与网络协议、磁盘格式无关：编码时按各自要求的位宽重新打包。

// section 是一个 16×16×16 的方块段。
type section struct {
	// storage 是方块状态的紧凑存储；零值表示全空气（不分配任何数组）。
	storage sectionStorage
	// nonAir 是非空气方块数量（Chunk Data 包的 block count，增量维护）。
	nonAir uint16
}

// blockState 返回 section 内局部索引（blockIndex）处的方块状态。
func (s *section) blockState(index int) uint16 {
	return s.storage.at(index)
}

// setBlock 写入 section 内局部索引处的方块状态，返回状态是否发生变化。
func (s *section) setBlock(index int, state uint16) bool {
	previous := s.storage.at(index)
	if previous == state {
		return false
	}
	if previous == AirBlock {
		s.nonAir++
	} else if state == AirBlock {
		s.nonAir--
	}
	if s.nonAir == 0 {
		// 挖空后回退为零值（全空气）存储，释放位流数组；
		// 后续写入会自动重建，语义与全空气一致。
		s.storage = sectionStorage{}
		return true
	}
	s.storage.set(index, state)
	return true
}

// sectionStorage 是 section 方块状态的紧凑存储。
//
// bits 的取值：
//   - 0：整个 section 为同一方块（uniform；零值 = 全空气，palette/data 均为 nil）；
//   - 1/2/4/8：调色板索引的紧密位流（索引 i 位于第 (i*bits)/64 个 uint64 的
//     第 (i*bits)%64 位起；位宽为 2 的幂，单个条目不会跨越 uint64 边界）；
//   - 16：调色板超过 256 项时的直接存储（全局方块状态 ID）。
//
// 位宽按内存从小到大的顺序增长（1→2→4→8→16），只在调色板容量不足时
// 重新打包。方块种类少的地形 section 永远停留在 1–2 位。
type sectionStorage struct {
	// palette 是 bits ≤ 8 时的方块状态调色板；bits == 16 时为 nil。
	palette []uint16
	// data 是打包的调色板索引或直接状态；bits == 0 时为 nil。
	data []uint64
	// uniform 是 bits == 0 时的统一方块状态（零值 = 空气）。
	uniform uint16
	// bits 是每个条目的位宽（0/1/2/4/8/16）。
	bits uint8
}

// allAir 报告存储是否表示全空气。
func (st *sectionStorage) allAir() bool {
	return st.bits == 0 && st.uniform == AirBlock
}

// at 返回第 i 个方块状态。
func (st *sectionStorage) at(i int) uint16 {
	switch st.bits {
	case 0:
		return st.uniform
	case 16:
		return uint16(st.wordAt(i))
	default:
		return st.palette[st.wordAt(i)]
	}
}

// wordAt 提取第 i 个打包条目（调色板索引或直接状态）。
func (st *sectionStorage) wordAt(i int) int {
	bits := int(st.bits)
	position := i * bits
	return int(st.data[position>>6]>>uint(position&63)) & (1<<uint(bits) - 1)
}

// set 写入第 i 个方块状态；调用方保证 value 是有效状态且与 at(i) 不同。
func (st *sectionStorage) set(i int, value uint16) {
	switch st.bits {
	case 0:
		// uniform 升级为两位调色板（1 位/方块 = 512 字节）。
		st.palette = []uint16{st.uniform, value}
		st.bits = 1
		st.data = make([]uint64, SectionVolume/64)
		st.setWord(i, 1)
	case 16:
		st.setWord(i, int(value))
	default:
		for index, entry := range st.palette {
			if entry == value {
				st.setWord(i, index)
				return
			}
		}
		if len(st.palette) < 1<<uint(st.bits) {
			index := len(st.palette)
			st.palette = append(st.palette, value)
			st.setWord(i, index)
			return
		}
		// 调色板已满：位宽提升一档后重试。
		st.grow()
		st.set(i, value)
	}
}

// setWord 覆盖第 i 个打包条目。
func (st *sectionStorage) setWord(i, value int) {
	bits := int(st.bits)
	position := i * bits
	shift := uint(position & 63)
	mask := uint64(1<<uint(bits)-1) << shift
	st.data[position>>6] = st.data[position>>6]&^mask | uint64(value)<<shift
}

// uniformState 报告所有打包条目是否相同，相同时返回对应的方块状态。
// 用于网络编码时把“所有位置同一种方块”的 section 压缩为单值调色板
// （紧凑存储可能含历史遗留的未使用条目，导致位宽无法直接反映这一点）。
func (st *sectionStorage) uniformState() (uint16, bool) {
	if st.bits == 0 {
		return st.uniform, true
	}
	bits := int(st.bits)
	mask := uint64(1)<<uint(bits) - 1
	value := st.data[0] & mask
	// 所有条目相同 ⇔ 每个 64 位字都等于“该索引重复填满一个字”。
	fill := value
	for shift := bits; shift < 64; shift += bits {
		fill |= value << uint(shift)
	}
	for _, word := range st.data {
		if word != fill {
			return 0, false
		}
	}
	if bits == 16 {
		return uint16(value), true
	}
	return st.palette[value], true
}

// grow 把位宽提升一档：1→2→4→8→16；8→16 时改为直接存储（无调色板）。
//
// 8 位且调色板已满时，先移除历史遗留的未使用条目（方块类型曾出现但
// 现已不存在于任一位置）：若因此腾出空间，保持 8 位即可，避免为了
// 少量在用状态升级到 8 KiB 的直接存储。
func (st *sectionStorage) grow() {
	if st.bits == 8 {
		var used [256]bool
		usedCount := 0
		for i := 0; i < SectionVolume; i++ {
			index := st.wordAt(i)
			if !used[index] {
				used[index] = true
				usedCount++
			}
		}
		if usedCount < 256 {
			remap := make([]int, 256)
			compacted := make([]uint16, 0, usedCount)
			for index, state := range st.palette {
				if used[index] {
					remap[index] = len(compacted)
					compacted = append(compacted, state)
				}
			}
			for i := 0; i < SectionVolume; i++ {
				position := i * 8
				word := position >> 6
				shift := uint(position & 63)
				st.data[word] = st.data[word]&^(uint64(0xFF)<<shift) | uint64(remap[st.wordAt(i)])<<shift
			}
			st.palette = compacted
			return
		}
		data := make([]uint64, SectionVolume*16/64)
		for i := 0; i < SectionVolume; i++ {
			position := i * 16
			if value := st.wordAt(i); value != 0 {
				data[position>>6] |= uint64(st.palette[value]) << uint(position&63)
			}
		}
		st.palette = nil
		st.bits = 16
		st.data = data
		return
	}
	newBits := int(st.bits) * 2
	data := make([]uint64, SectionVolume*newBits/64)
	for i := 0; i < SectionVolume; i++ {
		position := i * newBits
		if value := st.wordAt(i); value != 0 {
			data[position>>6] |= uint64(value) << uint(position&63)
		}
	}
	st.bits = uint8(newBits)
	st.data = data
}
