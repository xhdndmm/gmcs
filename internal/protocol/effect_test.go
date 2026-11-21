package protocol

import "testing"

func TestEncodeMobEffectPackets(t *testing.T) {
	update := EncodeUpdateMobEffect(42, 9, 2, 600, MobEffectFlagVisible|MobEffectFlagShowIcon)
	id, offset, err := DecodeVarInt(update)
	if err != nil || id != PlayPacketIDUpdateMobEffect {
		t.Fatalf("update id = %d, err=%v", id, err)
	}
	entityID, offset, err := decodeVarIntAt(update, offset)
	if err != nil || entityID != 42 {
		t.Fatalf("entity = %d, err=%v", entityID, err)
	}
	effectID, offset, err := decodeVarIntAt(update, offset)
	if err != nil || effectID != 9 {
		t.Fatalf("effect = %d, err=%v", effectID, err)
	}
	amplifier, offset, err := decodeVarIntAt(update, offset)
	if err != nil || amplifier != 2 {
		t.Fatalf("amplifier = %d, err=%v", amplifier, err)
	}
	duration, offset, err := decodeVarIntAt(update, offset)
	if err != nil || duration != 600 {
		t.Fatalf("duration = %d, err=%v", duration, err)
	}
	if offset >= len(update) || update[offset] != MobEffectFlagVisible|MobEffectFlagShowIcon {
		t.Fatalf("flags = %#x", update[offset:])
	}
	remove := EncodeRemoveMobEffect(42, 9)
	id, offset, err = DecodeVarInt(remove)
	if err != nil || id != PlayPacketIDRemoveMobEffect {
		t.Fatalf("remove id = %d, err=%v", id, err)
	}
	entityID, offset, err = decodeVarIntAt(remove, offset)
	if err != nil || entityID != 42 {
		t.Fatalf("remove entity = %d, err=%v", entityID, err)
	}
	effectID, _, err = decodeVarIntAt(remove, offset)
	if err != nil || effectID != 9 {
		t.Fatalf("remove effect = %d, err=%v", effectID, err)
	}
}
