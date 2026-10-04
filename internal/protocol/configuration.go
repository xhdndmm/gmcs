package protocol

import "fmt"

// 1.21.11（协议 774）Configuration 阶段的包 ID。
const (
	ConfigPacketIDClientInformation     = 0x00 // serverbound
	ConfigPacketIDPluginMessage         = 0x01 // clientbound
	ConfigPacketIDFinishConfiguration   = 0x03
	ConfigPacketIDKeepAlive             = 0x04
	ConfigPacketIDPong                  = 0x05 // serverbound
	ConfigPacketIDRegistryData          = 0x07 // clientbound
	ConfigServerboundPacketIDKnownPacks = 0x07
	ConfigPacketIDFeatureFlags          = 0x0C // clientbound
	ConfigPacketIDTags                  = 0x0D // clientbound
	ConfigPacketIDKnownPacks            = 0x0E // clientbound
)

// KnownPack 是 Known Packs 协商中的一个数据包条目。
type KnownPack struct {
	Namespace string
	ID        string
	Version   string
}

// ClientInformation 保存客户端上报的选项。
type ClientInformation struct {
	Locale              string
	ViewDistance        int8
	ChatMode            int32
	ChatColors          bool
	SkinParts           uint8
	MainHand            int32
	EnableTextFiltering bool
	EnableServerListing bool
	ParticleStatus      int32
}

// RegistryEntry 是一个同步注册表条目；Data 为 nil 表示内容由 Known Packs 提供。
type RegistryEntry struct {
	Name string
	Data []byte
}

// TagEntry 是一个标签及其注册表条目 ID（已展开嵌套引用）。
type TagEntry struct {
	Name    string
	Entries []int32
}

// TagRegistry 是一个注册表及其全部标签（用于 Update Tags）。
type TagRegistry struct {
	Registry string
	Tags     []TagEntry
}

// EncodeBrand 编码 Brand 插件消息（minecraft:brand 通道）。
func EncodeBrand(brand string) []byte {
	packet := AppendVarInt(nil, int32(ConfigPacketIDPluginMessage))
	packet = appendString(packet, "minecraft:brand")
	packet = appendString(packet, brand)
	return packet
}

// EncodeFeatureFlags 编码启用的功能开关列表。
func EncodeFeatureFlags(flags []string) []byte {
	packet := AppendVarInt(nil, int32(ConfigPacketIDFeatureFlags))
	packet = AppendVarInt(packet, int32(len(flags)))
	for _, flag := range flags {
		packet = appendString(packet, flag)
	}
	return packet
}

// EncodeKnownPacks 编码服务端已知的数据包列表。
func EncodeKnownPacks(packs []KnownPack) []byte {
	packet := AppendVarInt(nil, int32(ConfigPacketIDKnownPacks))
	packet = AppendVarInt(packet, int32(len(packs)))
	for _, pack := range packs {
		packet = appendString(packet, pack.Namespace)
		packet = appendString(packet, pack.ID)
		packet = appendString(packet, pack.Version)
	}
	return packet
}

// ParseKnownPacksResponse 解析客户端返回的 Known Packs 响应。
func ParseKnownPacksResponse(packet []byte) ([]KnownPack, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != ConfigServerboundPacketIDKnownPacks {
		return nil, fmt.Errorf("invalid known packs response packet")
	}
	count, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || count < 0 || count > 64 {
		return nil, fmt.Errorf("invalid known packs count")
	}
	packs := make([]KnownPack, 0, count)
	for i := int32(0); i < count; i++ {
		var pack KnownPack
		if pack.Namespace, offset, err = readStringAt(packet, offset); err != nil {
			return nil, fmt.Errorf("read known pack namespace: %w", err)
		}
		if pack.ID, offset, err = readStringAt(packet, offset); err != nil {
			return nil, fmt.Errorf("read known pack id: %w", err)
		}
		if pack.Version, offset, err = readStringAt(packet, offset); err != nil {
			return nil, fmt.Errorf("read known pack version: %w", err)
		}
		packs = append(packs, pack)
	}
	if offset != len(packet) {
		return nil, fmt.Errorf("trailing data in known packs response")
	}
	return packs, nil
}

// EncodeRegistryData 编码 Registry Data 包。
// 当条目 Data 为 nil 时省略 NBT 内容，客户端将从协商过的 Known Packs 中读取。
func EncodeRegistryData(registry string, entries []RegistryEntry) []byte {
	packet := AppendVarInt(nil, int32(ConfigPacketIDRegistryData))
	packet = appendString(packet, registry)
	packet = AppendVarInt(packet, int32(len(entries)))
	for _, entry := range entries {
		packet = appendString(packet, entry.Name)
		if entry.Data == nil {
			packet = append(packet, 0x00)
			continue
		}
		packet = append(packet, 0x01)
		packet = append(packet, entry.Data...)
	}
	return packet
}

// EncodeFinishConfiguration 编码 Finish Configuration 包。
// 该包发送后连接切换到 Play 阶段。
func EncodeFinishConfiguration() []byte {
	return AppendVarInt(nil, int32(ConfigPacketIDFinishConfiguration))
}

// EncodeUpdateTags 编码 Update Tags 包。
// 标签数据不能从 Known Packs 获取，配置阶段必须完整发送同步注册表的标签。
func EncodeUpdateTags(registries []TagRegistry) []byte {
	packet := AppendVarInt(nil, int32(ConfigPacketIDTags))
	packet = AppendVarInt(packet, int32(len(registries)))
	for _, registry := range registries {
		packet = appendString(packet, registry.Registry)
		packet = AppendVarInt(packet, int32(len(registry.Tags)))
		for _, tag := range registry.Tags {
			packet = appendString(packet, tag.Name)
			packet = AppendVarInt(packet, int32(len(tag.Entries)))
			for _, id := range tag.Entries {
				packet = AppendVarInt(packet, id)
			}
		}
	}
	return packet
}

// ParseClientInformation 解析客户端信息包（配置与 Play 阶段共用相同结构）。
func ParseClientInformation(packet []byte) (ClientInformation, error) {
	var info ClientInformation
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || (packetID != ConfigPacketIDClientInformation && packetID != PlayServerboundPacketIDClientInformation) {
		return info, fmt.Errorf("invalid client information packet")
	}
	if info.Locale, offset, err = readStringAt(packet, offset); err != nil {
		return info, fmt.Errorf("read locale: %w", err)
	}
	if len(packet) <= offset {
		return info, fmt.Errorf("missing view distance")
	}
	info.ViewDistance = int8(packet[offset])
	offset++
	if info.ChatMode, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return info, fmt.Errorf("read chat mode: %w", err)
	}
	if info.ChatColors, offset, err = DecodeBool(packet, offset); err != nil {
		return info, fmt.Errorf("read chat colors: %w", err)
	}
	if len(packet) <= offset {
		return info, fmt.Errorf("missing skin parts")
	}
	info.SkinParts = packet[offset]
	offset++
	if info.MainHand, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return info, fmt.Errorf("read main hand: %w", err)
	}
	if info.EnableTextFiltering, offset, err = DecodeBool(packet, offset); err != nil {
		return info, fmt.Errorf("read text filtering flag: %w", err)
	}
	if info.EnableServerListing, offset, err = DecodeBool(packet, offset); err != nil {
		return info, fmt.Errorf("read server listing flag: %w", err)
	}
	if info.ParticleStatus, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return info, fmt.Errorf("read particle status: %w", err)
	}
	if offset != len(packet) {
		return info, fmt.Errorf("trailing data in client information packet")
	}
	return info, nil
}

// ParseFinishConfigurationAck 校验 Acknowledge Finish Configuration 包。
func ParseFinishConfigurationAck(packet []byte) error {
	packetID, size, err := DecodeVarInt(packet)
	if err != nil || packetID != ConfigPacketIDFinishConfiguration || size != len(packet) {
		return fmt.Errorf("invalid acknowledge finish configuration packet")
	}
	return nil
}

// ParseConfigurationKeepAlive 解析配置阶段的 Keep Alive 包。
func ParseConfigurationKeepAlive(packet []byte) (int64, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != ConfigPacketIDKeepAlive {
		return 0, fmt.Errorf("invalid keep alive packet")
	}
	value, _, err := DecodeInt64(packet, offset)
	if err != nil {
		return 0, err
	}
	return value, nil
}

// ParseConfigurationPong 解析配置阶段的 Pong 包。
func ParseConfigurationPong(packet []byte) (int32, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != ConfigPacketIDPong {
		return 0, fmt.Errorf("invalid pong packet")
	}
	if len(packet)-offset < 4 {
		return 0, fmt.Errorf("invalid pong payload")
	}
	value, _, err := DecodeInt32(packet, offset)
	return value, err
}
