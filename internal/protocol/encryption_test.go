package protocol

import (
	"bytes"
	"crypto/aes"
	"encoding/hex"
	"io"
	"testing"
)

// 参考向量由 openssl 1.x（openssl enc -aes-128-cfb8）生成。
func TestCFB8KnownVector(t *testing.T) {
	key, err := hex.DecodeString("2b7e151628aed2a6abf7158809cf4f3c")
	if err != nil {
		t.Fatal(err)
	}
	iv, err := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := hex.DecodeString("6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e51")
	if err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString("3b79424c9c0dd436bace9e0ed4586a4f32b9ded50ae3ba69d472e88267fb5052")
	if err != nil {
		t.Fatal(err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plaintext))
	NewCFB8Encrypter(block, iv).XORKeyStream(ciphertext, plaintext)
	if !bytes.Equal(ciphertext, want) {
		t.Fatalf("ciphertext mismatch:\n got %x\nwant %x", ciphertext, want)
	}

	decrypted := make([]byte, len(ciphertext))
	NewCFB8Decrypter(block, iv).XORKeyStream(decrypted, ciphertext)
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatal("decryption mismatch")
	}

	// 就地解密（dst == src）也必须正确。
	inPlace := append([]byte(nil), ciphertext...)
	NewCFB8Decrypter(block, iv).XORKeyStream(inPlace, inPlace)
	if !bytes.Equal(inPlace, plaintext) {
		t.Fatal("in-place decryption mismatch")
	}
}

// 参考值由 Python（int.from_bytes(..., signed=True) + format(x)）生成，
// 即 Java BigInteger 的有符号十六进制表示。
func TestServerHash(t *testing.T) {
	secret := make([]byte, 16)
	for i := range secret {
		secret[i] = byte(i)
	}
	publicKey := append([]byte{0x30, 0x81, 0x9F}, ascendingBytes(1, 160)...)
	if got := ServerHash("", secret, publicKey); got != "4a86987b3e51e710dbd6554952776f8e888ca99c" {
		t.Fatalf("hash = %s", got)
	}
	negative := ServerHash("test", bytes.Repeat([]byte{0xAA}, 16), bytes.Repeat([]byte{0x11}, 64))
	if negative != "-51670ff50861653048244a68bc1fb8173f9979b7" {
		t.Fatalf("hash2 = %s", negative)
	}
}

func ascendingBytes(start, count int) []byte {
	out := make([]byte, count)
	for i := range out {
		out[i] = byte(start + i)
	}
	return out
}

func TestEncodeEncryptionRequest(t *testing.T) {
	publicKey := []byte{1, 2, 3}
	token := []byte{4, 5, 6, 7}
	packet := EncodeEncryptionRequest("", publicKey, token, true)

	offset := 0
	packetID, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || packetID != LoginPacketIDEncryptionRequest {
		t.Fatalf("packet id %#x (err=%v)", packetID, err)
	}
	serverID, offset, err := readStringAt(packet, offset)
	if err != nil || serverID != "" {
		t.Fatalf("server id %q (err=%v)", serverID, err)
	}
	gotKey, offset, err := readByteArrayAt(packet, offset)
	if err != nil || !bytes.Equal(gotKey, publicKey) {
		t.Fatalf("public key %x (err=%v)", gotKey, err)
	}
	gotToken, offset, err := readByteArrayAt(packet, offset)
	if err != nil || !bytes.Equal(gotToken, token) {
		t.Fatalf("token %x (err=%v)", gotToken, err)
	}
	if offset+1 != len(packet) || packet[offset] != 0x01 {
		t.Fatalf("should authenticate flag: % x", packet[offset:])
	}
}

func TestParseEncryptionResponse(t *testing.T) {
	secret := bytes.Repeat([]byte{0x42}, 16)
	token := []byte{9, 9}
	packet := AppendVarInt(nil, LoginServerboundPacketIDEncryptionResponse)
	packet = appendByteArray(packet, secret)
	packet = appendByteArray(packet, token)
	gotSecret, gotToken, err := ParseEncryptionResponse(packet)
	if err != nil || !bytes.Equal(gotSecret, secret) || !bytes.Equal(gotToken, token) {
		t.Fatalf("decoded %x %x (err=%v)", gotSecret, gotToken, err)
	}
	if _, _, err := ParseEncryptionResponse(AppendVarInt(nil, 0x00)); err == nil {
		t.Fatal("expected wrong packet ID to be rejected")
	}
}

func TestStreamReaderWriterRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 16)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	iv := bytes.Repeat([]byte{0x22}, aes.BlockSize)

	var buf bytes.Buffer
	plain := []byte("hello encrypted world, hello encrypted world")
	writer := NewStreamWriter(&buf, NewCFB8Encrypter(block, iv))
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("hello")) {
		t.Fatal("payload was not encrypted")
	}

	reader := NewStreamReader(&buf, NewCFB8Decrypter(block, iv))
	out := make([]byte, len(plain))
	if _, err := io.ReadFull(reader, out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("round trip mismatch")
	}
}
