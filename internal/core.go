package internal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"

	"github.com/bytemare/cryptotools/encoding"
	"github.com/bytemare/cryptotools/group"
	"github.com/bytemare/cryptotools/hash"
	"github.com/bytemare/cryptotools/signature"
	"github.com/bytemare/cryptotools/utils"
	"github.com/bytemare/opaque/envelope"
	"github.com/bytemare/opaque/message"
)

const (
	labelPrefix  = "OPAQUE"
	tagHandshake = "handshake secret"
	tagSession   = "session secret"
	tagMacServer = "server mac"
	tagMacClient = "client mac"
	tagEncServer = "server enc"
	tagEncClient = "client enc"

	aeadNonceSize   = 16
	AesGcmKeyLength = 32
)

type Core struct {
	group.Group
	*hash.Hash
	NonceU        []byte
	NonceS        []byte
	Esk           group.Scalar
	Epk           group.Element
	Km2, Km3      []byte
	Ke2           []byte
	SessionSecret []byte
	Transcript2   []byte
	Transcript3   []byte
	SigmaServer
}

type SigmaServer struct {
	signature.Identifier
	Idu, Pku []byte
}

type Metadata struct {
	CredReq, CredResp []byte
	IDu, IDs, Info1   []byte
	KeyLen            int
}

func (m *Metadata) Fill(creds message.Credentials, cresp *message.CredentialResponse, pku []byte, enc encoding.Encoding) error {
	if creds.EnvelopeMode() == envelope.CustomIdentifier {
		m.IDu = creds.UserID()
		m.IDs = creds.ServerID()
	}

	if m.IDu == nil {
		m.IDu = pku
	}

	if m.IDs == nil {
		m.IDs = creds.ServerPublicKey()
	}

	encCresp, err := enc.Encode(cresp)
	if err != nil {
		panic(err)
	}

	m.CredResp = encCresp
	m.KeyLen = AesGcmKeyLength

	return nil
}

func (c *Core) DeriveKeys(m *Metadata, tag, nonceU, nonceS, ikm []byte) {
	info := info(tag, nonceU, nonceS, m.IDu, m.IDs)
	handshakeSecret, sessionSecret := keySchedule(c.Hash, ikm, info)
	c.SessionSecret = sessionSecret
	c.Km2, c.Km3 = macKeys(c.Hash, handshakeSecret)
	c.Ke2 = hkdfExpandLabel(c.Hash, handshakeSecret, []byte(""), tagEncServer, m.KeyLen)
}

func lengthPrefixEncode(input []byte) []byte {
	return append(encoding.I2OSP2(uint(len(input))), input...)
}

func info(protoTag, nonceU, nonceS, idU, idS []byte) []byte {
	return utils.Concatenate(0, protoTag,
		lengthPrefixEncode(nonceU), lengthPrefixEncode(nonceS),
		lengthPrefixEncode(idU), lengthPrefixEncode(idS))
}

func buildLabel(label string) []byte {
	return []byte(labelPrefix + label)
}

type HkdfLabel struct {
	length  uint16
	label   []byte
	context []byte // todo: what is this context ?
}

func hkdfExpand(h *hash.Hash, secret, hkdfLabel []byte, length int) []byte {
	return h.HKDFExpand(secret, hkdfLabel, length)
}

func hkdfExpandLabel(h *hash.Hash, secret, context []byte, label string, length int) []byte {
	return hkdfExpand(h, secret, buildLabel(label), length)
}

func deriveSecret(h *hash.Hash, secret, transcript []byte, label string) []byte {
	return hkdfExpandLabel(h, secret, h.Hash(0, transcript), label, h.OutputSize())
}

func keySchedule(h *hash.Hash, ikm, info []byte) (handshakeSecret, sessionSecret []byte) {
	handshakeSecret = deriveSecret(h, ikm, info, tagHandshake)
	sessionSecret = deriveSecret(h, ikm, info, tagSession)

	return
}

func macKeys(h *hash.Hash, handshakeSecret []byte) (km2, km3 []byte) {
	km2 = hkdfExpandLabel(h, handshakeSecret, []byte(""), tagMacServer, h.OutputSize())
	km3 = hkdfExpandLabel(h, handshakeSecret, []byte(""), tagMacClient, h.OutputSize())

	return
}

type Ke1 struct {
	NonceU []byte `json:"n"`
	EpkU   []byte `json:"e"`
}

func encode(k interface{}, enc encoding.Encoding) []byte {
	output, err := enc.Encode(k)
	if err != nil {
		panic(err)
	}

	return output
}

func (k Ke1) Encode(enc encoding.Encoding) []byte {
	return encode(k, enc)
}

func DecodeKe1(input []byte, enc encoding.Encoding) (*Ke1, error) {
	d, err := enc.Decode(input, &Ke1{})
	if err != nil {
		return nil, err
	}

	de, ok := d.(*Ke1)
	if !ok {
		return nil, ErrAssertKe1
	}

	return de, nil
}

func AesGcmEncrypt(key, plaintext []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err.Error())
	}

	nonce := make([]byte, aeadNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		panic(err.Error())
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err.Error())
	}

	return append(nonce, aesgcm.Seal(nil, nonce, plaintext, nil)...)
}

func AesGcmDecrypt(key, ciphertext []byte) ([]byte, error) {
	nonce := ciphertext[:aeadNonceSize]

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := aesgcm.Open(nil, nonce, ciphertext[aeadNonceSize:], nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}