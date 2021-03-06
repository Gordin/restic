package restic

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"io/ioutil"
	"sync"

	"golang.org/x/crypto/poly1305"
	"golang.org/x/crypto/scrypt"
)

const (
	AESKeySize = 32      // for AES256
	MACKeySize = 16 + 16 // for Poly1305-AES128
	ivSize     = aes.BlockSize
)

type AESKey [32]byte
type MACKey [32]byte
type IV [ivSize]byte

// mask for key, (cf. http://cr.yp.to/mac/poly1305-20050329.pdf)
var poly1305KeyMask = [16]byte{
	0xff,
	0xff,
	0xff,
	0x0f, // 3: top four bits zero
	0xfc, // 4: bottom two bits zero
	0xff,
	0xff,
	0x0f, // 7: top four bits zero
	0xfc, // 8: bottom two bits zero
	0xff,
	0xff,
	0x0f, // 11: top four bits zero
	0xfc, // 12: bottom two bits zero
	0xff,
	0xff,
	0x0f, // 15: top four bits zero
}

// key is a [32]byte, in the form k||r
func poly1305_sign(msg []byte, nonce []byte, key *MACKey) []byte {
	// prepare key for low-level poly1305.Sum(): r||n
	var k [32]byte

	// make sure key is masked
	maskKey(key)

	// fill in nonce, encrypted with AES and key[:16]
	cipher, err := aes.NewCipher(key[:16])
	if err != nil {
		panic(err)
	}
	cipher.Encrypt(k[16:], nonce[:])

	// copy r
	copy(k[:16], key[16:])

	// save mac in out
	var out [16]byte
	poly1305.Sum(&out, msg, &k)

	return out[:]
}

// mask poly1305 key
func maskKey(k *MACKey) {
	if k == nil {
		return
	}
	for i := 0; i < poly1305.TagSize; i++ {
		k[i+16] = k[i+16] & poly1305KeyMask[i]
	}
}

// key: k||r
func poly1305_verify(msg []byte, nonce []byte, key *MACKey, mac []byte) bool {
	// prepare key for low-level poly1305.Sum(): r||n
	var k [32]byte

	// make sure key is masked
	maskKey(key)

	// fill in nonce, encrypted with AES and key[:16]
	cipher, err := aes.NewCipher(key[:16])
	if err != nil {
		panic(err)
	}
	cipher.Encrypt(k[16:], nonce[:])

	// copy r
	copy(k[:16], key[16:])

	// copy mac to array
	var m [16]byte
	copy(m[:], mac)

	return poly1305.Verify(&m, msg, &k)
}

func generateRandomAESKey() (k *AESKey) {
	k = &AESKey{}
	n, err := rand.Read(k[:])
	if n != AESKeySize || err != nil {
		panic("unable to read enough random bytes for encryption key")
	}
	return
}

// returns [32]byte == k||r
func generateRandomMACKey() (k *MACKey) {
	k = &MACKey{}
	n, err := rand.Read(k[:])
	if n != MACKeySize || err != nil {
		panic("unable to read enough random bytes for mac key")
	}

	// mask r in second half
	maskKey(k)

	return
}

func generateRandomIV() (iv IV) {
	n, err := rand.Read(iv[:])
	if n != ivSize || err != nil {
		panic("unable to read enough random bytes for iv")
	}
	return
}

// Encrypt encrypts and signs data. Stored in ciphertext is IV || Ciphertext ||
// MAC. Encrypt returns the ciphertext's length.
func Encrypt(ks *keys, ciphertext, plaintext []byte) (int, error) {
	if cap(ciphertext) < len(plaintext)+ivSize+macSize {
		return 0, ErrBufferTooSmall
	}

	iv := generateRandomIV()
	copy(ciphertext, iv[:])

	c, err := aes.NewCipher(ks.Encrypt[:])
	if err != nil {
		panic(fmt.Sprintf("unable to create cipher: %v", err))
	}

	e := cipher.NewCTR(c, ciphertext[:ivSize])

	e.XORKeyStream(ciphertext[ivSize:cap(ciphertext)], plaintext)
	ciphertext = ciphertext[:ivSize+len(plaintext)]

	mac := poly1305_sign(ciphertext[ivSize:], ciphertext[:ivSize], ks.Sign)
	ciphertext = append(ciphertext, mac...)

	return len(ciphertext), nil
}

// Decrypt verifes and decrypts the ciphertext. Ciphertext must be in the form
// IV || Ciphertext || MAC.
func Decrypt(ks *keys, plaintext, ciphertext []byte) ([]byte, error) {
	// check for plausible length
	if len(ciphertext) < ivSize+macSize {
		panic("trying to decrypt invalid data: ciphertext too small")
	}

	if cap(plaintext) < len(ciphertext) {
		// extend plaintext
		plaintext = append(plaintext, make([]byte, len(ciphertext)-cap(plaintext))...)
	}

	// extract mac
	l := len(ciphertext) - macSize
	ciphertext, mac := ciphertext[:l], ciphertext[l:]

	// verify mac
	if !poly1305_verify(ciphertext[ivSize:], ciphertext[:ivSize], ks.Sign, mac) {
		return nil, ErrUnauthenticated
	}

	// extract iv
	iv, ciphertext := ciphertext[:ivSize], ciphertext[ivSize:]

	// decrypt data
	c, err := aes.NewCipher(ks.Encrypt[:])
	if err != nil {
		panic(fmt.Sprintf("unable to create cipher: %v", err))
	}

	// decrypt
	e := cipher.NewCTR(c, iv)
	plaintext = plaintext[:len(ciphertext)]
	e.XORKeyStream(plaintext, ciphertext)

	return plaintext, nil
}

// runs scrypt(password)
func kdf(k *Key, password string) (*keys, error) {
	if len(k.Salt) == 0 {
		return nil, fmt.Errorf("scrypt() called with empty salt")
	}

	keybytes := MACKeySize + AESKeySize
	scryptKeys, err := scrypt.Key([]byte(password), k.Salt, k.N, k.R, k.P, keybytes)
	if err != nil {
		return nil, fmt.Errorf("error deriving keys from password: %v", err)
	}

	if len(scryptKeys) != keybytes {
		return nil, fmt.Errorf("invalid numbers of bytes expanded from scrypt(): %d", len(scryptKeys))
	}

	ek := &AESKey{}
	copy(ek[:], scryptKeys[:AESKeySize])

	mk := &MACKey{}
	copy(mk[:], scryptKeys[AESKeySize:])

	ks := &keys{
		Encrypt: ek,
		Sign:    mk,
	}
	return ks, nil
}

type encryptWriter struct {
	iv      IV
	wroteIV bool
	data    *bytes.Buffer
	key     *keys
	s       cipher.Stream
	w       io.Writer
	origWr  io.Writer
	err     error // remember error writing iv
}

func (e *encryptWriter) Close() error {
	// write mac
	mac := poly1305_sign(e.data.Bytes()[ivSize:], e.data.Bytes()[:ivSize], e.key.Sign)
	_, err := e.origWr.Write(mac)
	if err != nil {
		return err
	}

	// return buffer
	FreeChunkBuf("EncryptWriter", e.data.Bytes())

	return nil
}

const encryptWriterChunkSize = 512 * 1024 // 512 KiB
var encryptWriterBufPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, encryptWriterChunkSize)
	},
}

func (e *encryptWriter) Write(p []byte) (int, error) {
	// write iv first
	if !e.wroteIV {
		_, e.err = e.origWr.Write(e.iv[:])
		e.wroteIV = true
	}

	if e.err != nil {
		return 0, e.err
	}

	buf := encryptWriterBufPool.Get().([]byte)
	defer encryptWriterBufPool.Put(buf)

	written := 0
	for len(p) > 0 {
		max := len(p)
		if max > encryptWriterChunkSize {
			max = encryptWriterChunkSize
		}

		e.s.XORKeyStream(buf, p[:max])
		n, err := e.w.Write(buf[:max])
		if n != max {
			if err == nil { // should never happen
				err = io.ErrShortWrite
			}
		}

		written += n
		p = p[n:]

		if err != nil {
			e.err = err
			return written, err
		}
	}

	return written, nil
}

// EncryptTo buffers data written to the returned io.WriteCloser. When Close()
// is called, the data is encrypted an written to the underlying writer.
func EncryptTo(ks *keys, wr io.Writer) io.WriteCloser {
	ew := &encryptWriter{
		iv:     generateRandomIV(),
		data:   bytes.NewBuffer(GetChunkBuf("EncryptWriter")[:0]),
		key:    ks,
		origWr: wr,
	}

	// buffer iv for mac
	_, err := ew.data.Write(ew.iv[:])
	if err != nil {
		panic(err)
	}

	c, err := aes.NewCipher(ks.Encrypt[:])
	if err != nil {
		panic(fmt.Sprintf("unable to create cipher: %v", err))
	}

	ew.s = cipher.NewCTR(c, ew.iv[:])
	ew.w = io.MultiWriter(ew.data, wr)

	return ew
}

type decryptReader struct {
	buf []byte
	pos int
}

func (d *decryptReader) Read(dst []byte) (int, error) {
	if d.buf == nil {
		return 0, io.EOF
	}

	if len(dst) == 0 {
		return 0, nil
	}

	remaining := len(d.buf) - d.pos
	if len(dst) >= remaining {
		n := copy(dst, d.buf[d.pos:])
		d.Close()
		return n, io.EOF
	}

	n := copy(dst, d.buf[d.pos:d.pos+len(dst)])
	d.pos += n

	return n, nil
}

func (d *decryptReader) ReadByte() (c byte, err error) {
	if d.buf == nil {
		return 0, io.EOF
	}

	remaining := len(d.buf) - d.pos
	if remaining == 1 {
		c = d.buf[d.pos]
		d.Close()
		return c, io.EOF
	}

	c = d.buf[d.pos]
	d.pos++

	return
}

func (d *decryptReader) Close() error {
	if d.buf == nil {
		return nil
	}

	FreeChunkBuf("decryptReader", d.buf)
	d.buf = nil
	return nil
}

// DecryptFrom verifies and decrypts the ciphertext read from rd with ks and
// makes it available on the returned Reader. Ciphertext must be in the form IV
// || Ciphertext || MAC. In order to correctly verify the ciphertext, rd is
// drained, locally buffered and made available on the returned Reader
// afterwards. If a MAC verification failure is observed, it is returned
// immediately.
func DecryptFrom(ks *keys, rd io.Reader) (io.ReadCloser, error) {
	ciphertext := GetChunkBuf("decryptReader")

	ciphertext = ciphertext[0:cap(ciphertext)]
	n, err := io.ReadFull(rd, ciphertext)
	if err != io.ErrUnexpectedEOF {
		// read remaining data
		buf, e := ioutil.ReadAll(rd)
		ciphertext = append(ciphertext, buf...)
		n += len(buf)
		err = e
	} else {
		err = nil
	}

	if err != nil {
		return nil, err
	}

	ciphertext = ciphertext[:n]

	// decrypt
	ciphertext, err = Decrypt(ks, ciphertext, ciphertext)
	if err != nil {
		return nil, err
	}

	return &decryptReader{buf: ciphertext}, nil
}
