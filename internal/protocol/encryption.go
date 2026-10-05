package protocol

import (
	"crypto/cipher"
	"crypto/sha1"
	"fmt"
	"io"
	"math/big"
)

// EncodeEncryptionRequest 编码 Encryption Request（官名 hello，1.20.5+ 含
// shouldAuthenticate 字段）。
func EncodeEncryptionRequest(serverID string, publicKey, verifyToken []byte, shouldAuthenticate bool) []byte {
	packet := AppendVarInt(nil, int32(LoginPacketIDEncryptionRequest))
	packet = appendString(packet, serverID)
	packet = appendByteArray(packet, publicKey)
	packet = appendByteArray(packet, verifyToken)
	return AppendBool(packet, shouldAuthenticate)
}

// ParseEncryptionResponse 解析 Encryption Response（官名 key）：
// 共享密钥与验证令牌（均由客户端用服务器公钥加密）。
func ParseEncryptionResponse(packet []byte) (sharedSecret, verifyToken []byte, err error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != LoginServerboundPacketIDEncryptionResponse {
		return nil, nil, fmt.Errorf("invalid encryption response packet")
	}
	sharedSecret, offset, err = readByteArrayAt(packet, offset)
	if err != nil {
		return nil, nil, err
	}
	verifyToken, _, err = readByteArrayAt(packet, offset)
	if err != nil {
		return nil, nil, err
	}
	return sharedSecret, verifyToken, nil
}

// ServerHash 计算原版会话服务器使用的 serverId 哈希：
// SHA-1(serverID + sharedSecret + publicKeyDER)，以十六进制大端有符号整数
// 表示（最高位为 1 时结果带负号前缀）。
func ServerHash(serverID string, sharedSecret, publicKey []byte) string {
	hash := sha1.New()
	hash.Write([]byte(serverID))
	hash.Write(sharedSecret)
	hash.Write(publicKey)
	digest := hash.Sum(nil)

	// 与 Java BigInteger（有符号大端）的十六进制表示一致。
	value := new(big.Int).SetBytes(digest)
	if value.Bit(159) == 1 {
		value.Sub(value, new(big.Int).Lsh(big.NewInt(1), 160))
	}
	return value.Text(16)
}

// cfb8 实现 Minecraft 使用的 AES/CFB8 流加密
// （标准库 crypto/cipher 只提供 CFB128）。
type cfb8 struct {
	block   cipher.Block
	iv      []byte
	tmp     []byte
	decrypt bool
}

// NewCFB8Encrypter 返回 AES/CFB8 加密流。
func NewCFB8Encrypter(block cipher.Block, iv []byte) cipher.Stream {
	return newCFB8(block, iv, false)
}

// NewCFB8Decrypter 返回 AES/CFB8 解密流。
func NewCFB8Decrypter(block cipher.Block, iv []byte) cipher.Stream {
	return newCFB8(block, iv, true)
}

func newCFB8(block cipher.Block, iv []byte, decrypt bool) *cfb8 {
	if len(iv) != block.BlockSize() {
		panic("protocol: CFB8 IV length must equal block size")
	}
	return &cfb8{
		block:   block,
		iv:      append([]byte(nil), iv...),
		tmp:     make([]byte, block.BlockSize()),
		decrypt: decrypt,
	}
}

// XORKeyStream 逐字节异或；每字节后把密文反馈回 IV 尾部。
// 支持 dst 与 src 为同一底层数组（就地加解密）。
func (c *cfb8) XORKeyStream(dst, src []byte) {
	for i := range src {
		in := src[i]
		c.block.Encrypt(c.tmp, c.iv)
		dst[i] = in ^ c.tmp[0]
		// 反馈字节：加密时为输出（密文），解密时为输入（密文）。
		feedback := in
		if !c.decrypt {
			feedback = dst[i]
		}
		copy(c.iv, c.iv[1:])
		c.iv[len(c.iv)-1] = feedback
	}
}

// StreamReader 在读取后对数据应用解密流（用于加密连接）。
type StreamReader struct {
	r      io.Reader
	stream cipher.Stream
}

// NewStreamReader 包装 reader，读取的数据会经 stream 解密。
func NewStreamReader(r io.Reader, stream cipher.Stream) *StreamReader {
	return &StreamReader{r: r, stream: stream}
}

func (s *StreamReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.stream.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

// StreamWriter 在写入前对数据应用加密流（用于加密连接）。
type StreamWriter struct {
	w      io.Writer
	stream cipher.Stream
}

// NewStreamWriter 包装 writer，写入的数据会经 stream 加密。
func NewStreamWriter(w io.Writer, stream cipher.Stream) *StreamWriter {
	return &StreamWriter{w: w, stream: stream}
}

func (s *StreamWriter) Write(p []byte) (int, error) {
	buf := make([]byte, len(p))
	s.stream.XORKeyStream(buf, p)
	written, err := s.w.Write(buf)
	if err == nil && written != len(buf) {
		// 部分写入后流状态已推进，连接无法安全恢复。
		err = io.ErrShortWrite
	}
	return written, err
}

// appendByteArray 追加 VarInt 长度前缀的字节数组。
func appendByteArray(dst, value []byte) []byte {
	dst = AppendVarInt(dst, int32(len(value)))
	return append(dst, value...)
}

// readByteArrayAt 读取 VarInt 长度前缀的字节数组。
func readByteArrayAt(data []byte, offset int) ([]byte, int, error) {
	length, size, err := DecodeVarInt(data[offset:])
	if err != nil {
		return nil, offset, err
	}
	if length < 0 || int(length) > len(data)-offset-size {
		return nil, offset, fmt.Errorf("byte array length out of range")
	}
	start := offset + size
	end := start + int(length)
	return data[start:end], end, nil
}
