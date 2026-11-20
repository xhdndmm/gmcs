//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package protocol

import (
	"os"
	"testing"

	"gmcs/internal/protocol/oracle"
	"gmcs/internal/registry"
)

// TestClientboundOracleExplosionPackets 用 1.21.11 客户端真实 StreamCodec 解码
// 爆炸相关包。默认跳过；设置 GMCS_ORACLE=1 并提供客户端 jar 后运行。
func TestClientboundOracleExplosionPackets(t *testing.T) {
	if os.Getenv("GMCS_ORACLE") == "" {
		t.Skip("set GMCS_ORACLE=1 to run the official client decoder oracle")
	}
	if !oracle.Available() {
		t.Skip("packet oracle unavailable: need GMCS_CLIENT_JAR and GMCS_MINECRAFT_LIBS")
	}
	tntType, ok := registry.StaticEntryID("minecraft:entity_type", "minecraft:tnt")
	if !ok {
		t.Fatal("missing minecraft:tnt entity type")
	}
	tntState, ok := registry.BlockStateIDs["minecraft:tnt"]
	if !ok {
		t.Fatal("missing minecraft:tnt block state")
	}
	explosionSound, ok := registry.StaticEntryID("minecraft:sound_event", "minecraft:entity.generic.explode")
	if !ok {
		t.Fatal("missing explosion sound event")
	}
	uuid := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	cases := []struct {
		name   string
		stage  string
		packet []byte
	}{
		{"add_entity", "play", EncodeAddEntity(42, uuid, tntType, 1.5, 64, 2.5, 0.1, 0.2, 0.3, 45, 10)},
		{"set_entity_data_fuse", "play", EncodeEntityMetadataVarInt(42, 8, 80)},
		{"set_entity_data_block_state", "play", EncodeEntityMetadataVarInt(42, 9, int32(tntState))},
		{"explode", "play", EncodeExplosion(1.5, 64, 2.5, 4, 7, ParticleExplosion)},
		{"block_update", "play", EncodeBlockUpdate(1, 64, 2, int32(tntState))},
		{"sound", "play", EncodeSoundEffect(explosionSound, 0, 1.5, 64, 2.5, 1, 1, 123)},
		{"entity_velocity", "play", EncodeEntityVelocity(42, 0.1, 0.2, 0.3)},
		{"remove_entities", "play", EncodeRemoveEntities([]int32{42, 43})},
		{"entity_event", "play", EncodeEntityEvent(42, 3)},
		{"set_health", "play", EncodeUpdateHealth(19.5, 20, 5)},
		{"player_combat_kill", "play", EncodePlayerCombatKill(42, "death.attack.generic")},
		{"configuration_brand", "configuration", EncodeBrand("gmcs")},
		{"configuration_finish", "configuration", EncodeFinishConfiguration()},
	}
	oracleCases := make([]oracle.Case, len(cases))
	for i, c := range cases {
		oracleCases[i] = oracle.Case{Stage: c.stage, Packet: c.packet}
	}
	results, err := oracle.CheckBatch(oracleCases)
	if err != nil {
		t.Fatal(err)
	}
	for i, result := range results {
		if !result.OK || result.Remaining != 0 {
			t.Errorf("%s: oracle rejected packet: %+v", cases[i].name, result)
		}
	}
}

// TestClientboundOracleRejectsTrailingBytes 验证 oracle 不是只“能解码”，还会
// 拒绝字段被截断/多出字节的包。Finish Configuration 没有负载，追加一个字节
// 必须被判定为剩余字节。
func TestClientboundOracleRejectsTrailingBytes(t *testing.T) {
	if os.Getenv("GMCS_ORACLE") == "" {
		t.Skip("set GMCS_ORACLE=1 to run the official client decoder oracle")
	}
	if !oracle.Available() {
		t.Skip("packet oracle unavailable: need GMCS_CLIENT_JAR and GMCS_MINECRAFT_LIBS")
	}
	packet := append(EncodeFinishConfiguration(), 0)
	results, err := oracle.CheckBatch([]oracle.Case{{Stage: "configuration", Packet: packet}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].OK || results[0].Remaining != 1 {
		t.Fatalf("trailing byte was not rejected: %+v", results)
	}
}
