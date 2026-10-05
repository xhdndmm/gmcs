package protocol

import (
	"encoding/binary"
	"math"
	"testing"
)

// readLpVec3 按 1.21.2+ 的 LpVec3 格式读取低精度向量。
// 参考实现：node-minecraft-protocol src/datatypes/lpVec3.js。
func readLpVec3(t *testing.T, data []byte, offset int) ([3]float64, int) {
	t.Helper()
	a := data[offset]
	if a == 0 {
		return [3]float64{}, offset + 1
	}
	b := data[offset+1]
	c := binary.BigEndian.Uint32(data[offset+2:])
	packed := uint64(c)*65536 + uint64(b)<<8 + uint64(a)
	scale := float64(a & 3)
	size := 6
	if a&4 == 4 {
		varInt, varSize, err := DecodeVarInt(data[offset+6:])
		if err != nil {
			t.Fatalf("decode lpVec3 continuation: %v", err)
		}
		scale = float64(varInt)*4 + float64(a&3)
		size = 6 + varSize
	}
	unpack := func(shift uint) float64 {
		quantized := math.Min(float64((packed>>shift)&0x7FFF), 32766)
		return (quantized*2)/32766 - 1
	}
	return [3]float64{unpack(3) * scale, unpack(18) * scale, unpack(33) * scale}, offset + size
}

func TestEncodeAddEntity(t *testing.T) {
	uuid := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	packet := EncodeAddEntity(42, uuid, 150, 1.5, 64, -2.25, 0.01, -0.02, 0.03, 90, -30)
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDAddEntity {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	entityID, tail, err := DecodeVarInt(packet[offset:])
	if err != nil || entityID != 42 {
		t.Fatalf("entity ID = %d (err=%v)", entityID, err)
	}
	offset += tail
	if string(packet[offset:offset+16]) != string(uuid[:]) {
		t.Fatalf("uuid mismatch: % x", packet[offset:offset+16])
	}
	offset += 16
	typeID, tail, err := DecodeVarInt(packet[offset:])
	if err != nil || typeID != 150 {
		t.Fatalf("type ID = %d (err=%v)", typeID, err)
	}
	offset += tail
	for i, want := range []float64{1.5, 64, -2.25} {
		value, next, err := DecodeFloat64(packet, offset)
		if err != nil || value != want {
			t.Fatalf("coordinate %d = %v (err=%v)", i, value, err)
		}
		offset = next
	}
	velocity, offset := readLpVec3(t, packet, offset)
	for i, want := range []float64{0.01, -0.02, 0.03} {
		if diff := math.Abs(velocity[i] - want); diff > 1e-4 {
			t.Fatalf("velocity %d = %v, want %v", i, velocity[i], want)
		}
	}
	// pitch -30° → 角度字节 round(-30 * 256 / 360) = -21
	if got := int8(packet[offset]); got != -21 {
		t.Fatalf("pitch byte = %d, want -21", got)
	}
	// yaw 90° → 64
	if got := int8(packet[offset+1]); got != 64 {
		t.Fatalf("yaw byte = %d, want 64", got)
	}
	// head yaw 与 yaw 相同
	if packet[offset+2] != packet[offset+1] {
		t.Fatalf("head yaw byte mismatch: %d", packet[offset+2])
	}
	offset += 3
	data, tail, err := DecodeVarInt(packet[offset:])
	if err != nil || data != 0 {
		t.Fatalf("object data = %d (err=%v)", data, err)
	}
	offset += tail
	if offset != len(packet) {
		t.Fatalf("trailing bytes: %d", len(packet)-offset)
	}
}

func TestEncodeEntityPositionSync(t *testing.T) {
	packet := EncodeEntityPositionSync(7, 1, 64, 2, 0.1, 0, -0.1, 45, -10, true)
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDEntityPositionSync {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	entityID, tail, err := DecodeVarInt(packet[offset:])
	if err != nil || entityID != 7 {
		t.Fatalf("entity ID = %d (err=%v)", entityID, err)
	}
	offset += tail
	wantValues := []float64{1, 64, 2, 0.1, 0, -0.1}
	for i, want := range wantValues {
		value, next, err := DecodeFloat64(packet, offset)
		if err != nil || value != want {
			t.Fatalf("value %d = %v (err=%v)", i, value, err)
		}
		offset = next
	}
	yaw, offset, err := DecodeFloat32(packet, offset)
	if err != nil || yaw != 45 {
		t.Fatalf("yaw = %v (err=%v)", yaw, err)
	}
	pitch, offset, err := DecodeFloat32(packet, offset)
	if err != nil || pitch != -10 {
		t.Fatalf("pitch = %v (err=%v)", pitch, err)
	}
	onGround, offset, err := DecodeBool(packet, offset)
	if err != nil || !onGround || offset != len(packet) {
		t.Fatalf("onGround = %v (err=%v, trailing=%d)", onGround, err, len(packet)-offset)
	}
}

func TestEncodeEntityDestroy(t *testing.T) {
	packet := EncodeEntityDestroy([]int32{3, 200, 1})
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDEntityDestroy {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	count, tail, err := DecodeVarInt(packet[offset:])
	if err != nil || count != 3 {
		t.Fatalf("count = %d (err=%v)", count, err)
	}
	offset += tail
	for _, want := range []int32{3, 200, 1} {
		id, tail, err := DecodeVarInt(packet[offset:])
		if err != nil || id != want {
			t.Fatalf("id = %d, want %d (err=%v)", id, want, err)
		}
		offset += tail
	}
	if offset != len(packet) {
		t.Fatalf("trailing bytes: %d", len(packet)-offset)
	}
}

func TestEncodeEntityEventAndHurtAnimation(t *testing.T) {
	event := EncodeEntityEvent(9, EntityEventDeath)
	packetID, offset, err := DecodeVarInt(event)
	if err != nil || packetID != PlayPacketIDEntityEvent {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	entityID, offset, err := DecodeInt32(event, offset)
	if err != nil || entityID != 9 || event[offset] != EntityEventDeath {
		t.Fatalf("entity event payload: id=%d status=%d err=%v", entityID, event[offset], err)
	}
	if len(event)-offset != 1 {
		t.Fatalf("unexpected trailing bytes: %d", len(event)-offset-1)
	}

	hurt := EncodeHurtAnimation(9, -90)
	packetID, offset, err = DecodeVarInt(hurt)
	if err != nil || packetID != PlayPacketIDHurtAnimation {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	if id, tail, err := DecodeVarInt(hurt[offset:]); err != nil || id != 9 {
		t.Fatalf("hurt entity ID = %d (err=%v)", id, err)
	} else {
		offset += tail
	}
	yaw, offset, err := DecodeFloat32(hurt, offset)
	if err != nil || yaw != -90 || offset != len(hurt) {
		t.Fatalf("hurt yaw = %v (err=%v, trailing=%d)", yaw, err, len(hurt)-offset)
	}
}

func TestEncodeDamageEvent(t *testing.T) {
	packet := EncodeDamageEvent(5, 28, 3, 3, nil)
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDDamageEvent {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	values := []int32{5, 28, 4, 4} // 进攻者 ID + 1
	for i, want := range values {
		value, tail, err := DecodeVarInt(packet[offset:])
		if err != nil || value != want {
			t.Fatalf("field %d = %d, want %d (err=%v)", i, value, want, err)
		}
		offset += tail
	}
	hasPosition, offset, err := DecodeBool(packet, offset)
	if err != nil || hasPosition || offset != len(packet) {
		t.Fatalf("hasPosition = %v (err=%v)", hasPosition, err)
	}

	position := [3]float64{1, 2, 3}
	packet = EncodeDamageEvent(5, 28, -1, -1, &position)
	_, offset, _ = DecodeVarInt(packet)
	for i := 0; i < 4; i++ {
		_, tail, err := DecodeVarInt(packet[offset:])
		if err != nil {
			t.Fatal(err)
		}
		offset += tail
	}
	hasPosition, offset, err = DecodeBool(packet, offset)
	if err != nil || !hasPosition {
		t.Fatalf("expected source position")
	}
	for i, want := range position {
		value, next, err := DecodeFloat64(packet, offset)
		if err != nil || value != want {
			t.Fatalf("position %d = %v, want %v (err=%v)", i, value, want, err)
		}
		offset = next
	}
	if offset != len(packet) {
		t.Fatalf("trailing bytes: %d", len(packet)-offset)
	}
}

func TestEncodeUpdateHealth(t *testing.T) {
	packet := EncodeUpdateHealth(18.5, 20, 5)
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDUpdateHealth {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	health, offset, err := DecodeFloat32(packet, offset)
	if err != nil || health != 18.5 {
		t.Fatalf("health = %v (err=%v)", health, err)
	}
	food, tail, err := DecodeVarInt(packet[offset:])
	if err != nil || food != 20 {
		t.Fatalf("food = %d (err=%v)", food, err)
	}
	offset += tail
	saturation, offset, err := DecodeFloat32(packet, offset)
	if err != nil || saturation != 5 || offset != len(packet) {
		t.Fatalf("saturation = %v (err=%v, trailing=%d)", saturation, err, len(packet)-offset)
	}
}

func TestEncodeRespawn(t *testing.T) {
	spawn := SpawnInfo{
		DimensionTypeID:  0,
		DimensionName:    "minecraft:overworld",
		HashedSeed:       -12345,
		GameMode:         1,
		PreviousGameMode: 0xFF,
		SeaLevel:         62,
	}
	packet := EncodeRespawn(spawn, 0)
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDRespawn {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	dimensionID, tail, err := DecodeVarInt(packet[offset:])
	if err != nil || dimensionID != 0 {
		t.Fatalf("dimension ID = %d (err=%v)", dimensionID, err)
	}
	offset += tail
	name, offset, err := readStringAt(packet, offset)
	if err != nil || name != "minecraft:overworld" {
		t.Fatalf("dimension name = %q (err=%v)", name, err)
	}
	seed, offset, err := DecodeInt64(packet, offset)
	if err != nil || seed != -12345 {
		t.Fatalf("seed = %d (err=%v)", seed, err)
	}
	if packet[offset] != 1 || packet[offset+1] != 0xFF {
		t.Fatalf("game modes = %d,%d", packet[offset], packet[offset+1])
	}
	offset += 2
	debug, offset, err := DecodeBool(packet, offset)
	if err != nil || debug {
		t.Fatal("expected debug=false")
	}
	flat, offset, err := DecodeBool(packet, offset)
	if err != nil || flat {
		t.Fatal("expected flat=false")
	}
	hasDeath, offset, err := DecodeBool(packet, offset)
	if err != nil || hasDeath {
		t.Fatal("expected death location=false")
	}
	cooldown, tail, err := DecodeVarInt(packet[offset:])
	if err != nil || cooldown != 0 {
		t.Fatalf("portal cooldown = %d (err=%v)", cooldown, err)
	}
	offset += tail
	seaLevel, tail, err := DecodeVarInt(packet[offset:])
	if err != nil || seaLevel != 62 {
		t.Fatalf("sea level = %d (err=%v)", seaLevel, err)
	}
	offset += tail
	if offset != len(packet)-1 || packet[offset] != 0 {
		t.Fatalf("copy metadata/flags mismatch: trailing=%d", len(packet)-offset)
	}
}

func TestEncodeSoundPackets(t *testing.T) {
	entity := EncodeEntitySoundEffect(1807, SoundCategoryHostile, 9, 1.5, 0.8, 42)
	packetID, offset, err := DecodeVarInt(entity)
	if err != nil || packetID != PlayPacketIDEntitySoundEffect {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	sound, tail, err := DecodeVarInt(entity[offset:])
	if err != nil || sound != 1807+1 { // Holder：ID+1
		t.Fatalf("sound holder = %d (err=%v)", sound, err)
	}
	offset += tail
	category, tail, err := DecodeVarInt(entity[offset:])
	if err != nil || category != SoundCategoryHostile {
		t.Fatalf("category = %d (err=%v)", category, err)
	}
	offset += tail
	entityID, tail, err := DecodeVarInt(entity[offset:])
	if err != nil || entityID != 9 {
		t.Fatalf("entity ID = %d (err=%v)", entityID, err)
	}
	offset += tail
	volume, offset, err := DecodeFloat32(entity, offset)
	if err != nil || volume != 1.5 {
		t.Fatalf("volume = %v (err=%v)", volume, err)
	}
	pitch, offset, err := DecodeFloat32(entity, offset)
	if err != nil || pitch != 0.8 {
		t.Fatalf("pitch = %v (err=%v)", pitch, err)
	}
	seed, offset, err := DecodeInt64(entity, offset)
	if err != nil || seed != 42 || offset != len(entity) {
		t.Fatalf("seed = %d (err=%v)", seed, err)
	}

	soundPacket := EncodeSoundEffect(1251, SoundCategoryPlayer, 1.5, 64, -2.5, 1, 1, 0)
	packetID, offset, err = DecodeVarInt(soundPacket)
	if err != nil || packetID != PlayPacketIDSoundEffect {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	holder, tail, err := DecodeVarInt(soundPacket[offset:])
	if err != nil || holder != 1251+1 {
		t.Fatalf("sound holder = %d (err=%v)", holder, err)
	}
	offset += tail
	category2, tail, err := DecodeVarInt(soundPacket[offset:])
	if err != nil || category2 != SoundCategoryPlayer {
		t.Fatalf("sound category = %d (err=%v)", category2, err)
	}
	offset += tail
	for i, want := range []int32{12, 512, -20} { // 坐标 ×8
		value, offset2, err := DecodeInt32(soundPacket, offset)
		if err != nil || value != want {
			t.Fatalf("coordinate %d = %d, want %d (err=%v)", i, value, want, err)
		}
		offset = offset2
	}
}

// TestEncodeInitializeWorldBorder 验证世界边界包的字段编码。
func TestEncodeInitializeWorldBorder(t *testing.T) {
	packet := EncodeInitializeWorldBorder(0, 0, 128, 128, 0, 29999984, 5, 15)
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDInitializeWorldBorder {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	for i, want := range []float64{0, 0, 128, 128} {
		value, next, err := DecodeFloat64(packet, offset)
		if err != nil || value != want {
			t.Fatalf("field %d = %v, want %v (err=%v)", i, value, want, err)
		}
		offset = next
	}
	for i, want := range []int32{0, 29999984, 5, 15} {
		value, tail, err := DecodeVarInt(packet[offset:])
		if err != nil || value != want {
			t.Fatalf("varint %d = %d, want %d (err=%v)", i, value, want, err)
		}
		offset += tail
	}
	if offset != len(packet) {
		t.Fatalf("trailing bytes: %d", len(packet)-offset)
	}
}

// TestAppendLpVec3 验证低精度向量的零值、常规值与 continuation 编码。
func TestAppendLpVec3(t *testing.T) {
	if packet := appendLpVec3(nil, 0, 0, 0); len(packet) != 1 || packet[0] != 0 {
		t.Fatalf("zero vector = % x", packet)
	}
	packet := appendLpVec3(nil, 10, -2, 0.5)
	if len(packet) != 7 || packet[0]&4 != 4 {
		t.Fatalf("large vector = % x", packet)
	}
	velocity, size := readLpVec3(t, packet, 0)
	if size != len(packet) {
		t.Fatalf("decoded size = %d, want %d", size, len(packet))
	}
	for i, want := range []float64{10, -2, 0.5} {
		if diff := math.Abs(velocity[i] - want); diff > 0.01 {
			t.Fatalf("component %d = %v, want %v", i, velocity[i], want)
		}
	}
}

func TestParseInteract(t *testing.T) {
	packet := AppendVarInt(nil, PlayServerboundPacketIDInteract)
	packet = AppendVarInt(packet, 17)
	packet = AppendVarInt(packet, InteractActionAttack)
	packet = AppendVarInt(packet, 0) // hand（attack 时客户端可能省略，解析器只读前两个字段）
	targetID, action, err := ParseInteract(packet)
	if err != nil || targetID != 17 || action != InteractActionAttack {
		t.Fatalf("interact = (%d, %d), err=%v", targetID, action, err)
	}
	if _, _, err := ParseInteract(AppendVarInt(nil, 0x77)); err == nil {
		t.Fatal("expected error for wrong packet ID")
	}
}

func TestParsePlayerMovement(t *testing.T) {
	position := AppendVarInt(nil, PlayServerboundPacketIDPlayerPosition)
	position = AppendFloat64(position, 1.5)
	position = AppendFloat64(position, 64)
	position = AppendFloat64(position, -2.5)
	position = append(position, 0x01)
	x, y, z, onGround, err := ParsePlayerPosition(position)
	if err != nil || x != 1.5 || y != 64 || z != -2.5 || !onGround {
		t.Fatalf("position = (%v, %v, %v, %v), err=%v", x, y, z, onGround, err)
	}

	rotation := AppendVarInt(nil, PlayServerboundPacketIDPlayerRotation)
	rotation = AppendFloat32(rotation, 90)
	rotation = AppendFloat32(rotation, -45)
	rotation = append(rotation, 0x00)
	yaw, pitch, err := ParsePlayerRotation(rotation)
	if err != nil || yaw != 90 || pitch != -45 {
		t.Fatalf("rotation = (%v, %v), err=%v", yaw, pitch, err)
	}

	look := AppendVarInt(nil, PlayServerboundPacketIDPlayerPositionRotation)
	look = AppendFloat64(look, 0.5)
	look = AppendFloat64(look, 65)
	look = AppendFloat64(look, 0.5)
	look = AppendFloat32(look, 180)
	look = AppendFloat32(look, 10)
	look = append(look, 0x00)
	x, y, z, yaw, pitch, onGround, err = ParsePlayerPositionRotation(look)
	if err != nil || x != 0.5 || y != 65 || z != 0.5 || yaw != 180 || pitch != 10 || onGround {
		t.Fatalf("position rotation = (%v,%v,%v,%v,%v,%v), err=%v", x, y, z, yaw, pitch, onGround, err)
	}

	// 缺少 flags 字节的截断包必须被拒绝。
	truncated := AppendVarInt(nil, PlayServerboundPacketIDPlayerPosition)
	truncated = AppendFloat64(truncated, 1)
	truncated = AppendFloat64(truncated, 64)
	truncated = AppendFloat64(truncated, 0)
	if _, _, _, _, err := ParsePlayerPosition(truncated); err == nil {
		t.Fatal("expected error for truncated position packet")
	}

	// NaN 坐标必须被拒绝。
	invalid := AppendVarInt(nil, PlayServerboundPacketIDPlayerPosition)
	invalid = AppendFloat64(invalid, math.NaN())
	invalid = AppendFloat64(invalid, 64)
	invalid = AppendFloat64(invalid, 0)
	invalid = append(invalid, 0x00)
	if _, _, _, _, err := ParsePlayerPosition(invalid); err == nil {
		t.Fatal("expected error for NaN coordinate")
	}
}

// TestEncodeEntityMetadataItem 验证物品实体 Item 元数据包
// （索引 8、类型 7 item_stack、以 0xFF 结束）。
func TestEncodeEntityMetadataItem(t *testing.T) {
	slot := AppendVarInt(nil, 5) // count
	slot = AppendVarInt(slot, 1) // item id
	slot = AppendVarInt(slot, 0) // added components
	slot = AppendVarInt(slot, 0) // removed components
	packet := EncodeEntityMetadataItem(9, slot)
	id, offset, err := DecodeVarInt(packet)
	if err != nil || id != PlayPacketIDEntityMetadata {
		t.Fatalf("metadata id = %#x (err=%v)", id, err)
	}
	entityID, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || entityID != 9 {
		t.Fatalf("entity id = %d (err=%v)", entityID, err)
	}
	if packet[offset] != EntityMetadataItemStackIndex {
		t.Fatalf("metadata index = %d, want %d", packet[offset], EntityMetadataItemStackIndex)
	}
	offset++
	metaType, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || metaType != 7 {
		t.Fatalf("metadata type = %d (err=%v)", metaType, err)
	}
	count, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || count != 5 {
		t.Fatalf("slot count = %d (err=%v)", count, err)
	}
	if _, offset, err = decodeVarIntAt(packet, offset); err != nil { // item id
		t.Fatal(err)
	}
	if _, offset, err = decodeVarIntAt(packet, offset); err != nil { // added components
		t.Fatal(err)
	}
	if _, offset, err = decodeVarIntAt(packet, offset); err != nil { // removed components
		t.Fatal(err)
	}
	if offset >= len(packet) || packet[offset] != 0xFF {
		t.Fatalf("metadata terminator missing at %d", offset)
	}
	if offset+1 != len(packet) {
		t.Fatalf("metadata trailing bytes: %d", len(packet)-offset-1)
	}
}
