package reporter

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strings"
)

const (
	runtimeRelaySeedBytes      = ed25519.SeedSize
	runtimeRelayIdentityBytes  = ed25519.PublicKeySize
	runtimeRelayNonceBytes     = 32
	runtimeRelaySignatureBytes = ed25519.SignatureSize
	runtimeRelayExchangeBytes  = 32
	runtimeRequestDirection    = byte(1)
	runtimeResponseDirection   = byte(2)
)

var errRuntimeFrameTooLarge = errors.New("runtime frame is too large")

type runtimeSession struct {
	aead cipher.AEAD
}

// RelayIdentity returns the public identity workers must authenticate before sending requests.
func (server *RuntimeServer) RelayIdentity() string {
	if server == nil {
		return ""
	}
	return server.relayIdentity
}

func runtimeRelayIdentity(seed []byte) (string, ed25519.PrivateKey, error) {
	if len(seed) != runtimeRelaySeedBytes {
		return "", nil, errors.New("runtime relay identity seed is invalid")
	}
	var nonzero byte
	for _, value := range seed {
		nonzero |= value
	}
	if nonzero == 0 {
		return "", nil, errors.New("runtime relay identity seed is invalid")
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return hex.EncodeToString(publicKey), privateKey, nil
}

func parseRuntimeRelayIdentity(encoded string) (ed25519.PublicKey, error) {
	decoded, err := hex.DecodeString(encoded)
	var nonzero byte
	for _, item := range decoded {
		nonzero |= item
	}
	if err != nil || len(decoded) != runtimeRelayIdentityBytes || hex.EncodeToString(decoded) != encoded || nonzero == 0 {
		return nil, errors.New("runtime relay identity is invalid")
	}
	return ed25519.PublicKey(decoded), nil
}

func authenticateRuntimeServerConnection(
	connection net.Conn,
	reader *bufio.Reader,
	privateKey ed25519.PrivateKey,
) (*runtimeSession, error) {
	encoded, err := reader.ReadSlice('\n')
	line := strings.TrimSuffix(string(encoded), "\n")
	parts := strings.Split(line, " ")
	if err != nil || len(parts) != 4 || parts[0] != runtimeProtocolVersion || parts[1] != "auth" {
		return nil, errors.New("serve runtime attachment: relay authentication failed")
	}
	nonce, nonceErr := hex.DecodeString(parts[2])
	clientPublicBytes, publicErr := hex.DecodeString(parts[3])
	curve := ecdh.X25519()
	clientPublic, keyErr := curve.NewPublicKey(clientPublicBytes)
	serverPrivate, generateErr := curve.GenerateKey(rand.Reader)
	if nonceErr != nil || len(nonce) != runtimeRelayNonceBytes || publicErr != nil ||
		len(clientPublicBytes) != runtimeRelayExchangeBytes || keyErr != nil || generateErr != nil {
		return nil, errors.New("serve runtime attachment: relay authentication failed")
	}
	serverPublicBytes := serverPrivate.PublicKey().Bytes()
	transcript := runtimeRelayTranscript(nonce, clientPublicBytes, serverPublicBytes)
	shared, err := serverPrivate.ECDH(clientPublic)
	if err != nil {
		return nil, errors.New("serve runtime attachment: relay authentication failed")
	}
	signature := ed25519.Sign(privateKey, transcript)
	proof := runtimeProtocolVersion + " proof " + hex.EncodeToString(serverPublicBytes) + " " + hex.EncodeToString(signature) + "\n"
	if _, err := io.WriteString(connection, proof); err != nil {
		return nil, errors.New("serve runtime attachment: relay authentication failed")
	}
	session, err := newRuntimeSession(shared, transcript)
	if err != nil {
		return nil, errors.New("serve runtime attachment: relay authentication failed")
	}
	return session, nil
}

func authenticateRuntimeClientConnection(connection net.Conn, publicKey ed25519.PublicKey) (*runtimeSession, error) {
	curve := ecdh.X25519()
	clientPrivate, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, errors.New("call runtime attachment: relay authentication failed")
	}
	nonce := make([]byte, runtimeRelayNonceBytes)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errors.New("call runtime attachment: relay authentication failed")
	}
	clientPublicBytes := clientPrivate.PublicKey().Bytes()
	request := runtimeProtocolVersion + " auth " + hex.EncodeToString(nonce) + " " + hex.EncodeToString(clientPublicBytes) + "\n"
	if _, err := io.WriteString(connection, request); err != nil {
		return nil, errors.New("call runtime attachment: relay authentication failed")
	}
	reader := bufio.NewReaderSize(connection, len(runtimeProtocolVersion)+runtimeRelaySignatureBytes*2+runtimeRelayExchangeBytes*2+24)
	encoded, err := reader.ReadSlice('\n')
	line := strings.TrimSuffix(string(encoded), "\n")
	parts := strings.Split(line, " ")
	if err != nil || len(parts) != 4 || parts[0] != runtimeProtocolVersion || parts[1] != "proof" {
		return nil, errors.New("call runtime attachment: relay authentication failed")
	}
	serverPublicBytes, publicErr := hex.DecodeString(parts[2])
	signature, signatureErr := hex.DecodeString(parts[3])
	serverPublic, keyErr := curve.NewPublicKey(serverPublicBytes)
	transcript := runtimeRelayTranscript(nonce, clientPublicBytes, serverPublicBytes)
	if publicErr != nil || len(serverPublicBytes) != runtimeRelayExchangeBytes || signatureErr != nil ||
		len(signature) != runtimeRelaySignatureBytes || keyErr != nil || !ed25519.Verify(publicKey, transcript, signature) {
		return nil, errors.New("call runtime attachment: relay authentication failed")
	}
	shared, err := clientPrivate.ECDH(serverPublic)
	if err != nil {
		return nil, errors.New("call runtime attachment: relay authentication failed")
	}
	session, err := newRuntimeSession(shared, transcript)
	if err != nil {
		return nil, errors.New("call runtime attachment: relay authentication failed")
	}
	return session, nil
}

func runtimeRelayTranscript(nonce, clientPublic, serverPublic []byte) []byte {
	transcript := make([]byte, 0, len(runtimeProtocolVersion)+len(nonce)+len(clientPublic)+len(serverPublic)+3)
	transcript = append(transcript, runtimeProtocolVersion...)
	transcript = append(transcript, 0)
	transcript = append(transcript, nonce...)
	transcript = append(transcript, 0)
	transcript = append(transcript, clientPublic...)
	transcript = append(transcript, 0)
	return append(transcript, serverPublic...)
}

func newRuntimeSession(shared, transcript []byte) (*runtimeSession, error) {
	material := make([]byte, 0, len(runtimeProtocolVersion)+len(shared)+len(transcript)+2)
	material = append(material, runtimeProtocolVersion...)
	material = append(material, 0)
	material = append(material, shared...)
	material = append(material, 0)
	material = append(material, transcript...)
	key := sha256.Sum256(material)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &runtimeSession{aead: aead}, nil
}

func writeRuntimeFrame(connection net.Conn, session *runtimeSession, direction byte, plaintext []byte) error {
	nonce := make([]byte, session.aead.NonceSize())
	nonce[len(nonce)-1] = direction
	sealed := session.aead.Seal(nil, nonce, plaintext, runtimeFrameAdditionalData(direction))
	encoded := base64.RawStdEncoding.EncodeToString(sealed) + "\n"
	_, err := io.WriteString(connection, encoded)
	return err
}

func readRuntimeFrame(reader *bufio.Reader, session *runtimeSession, direction byte, maximumPlaintext int) ([]byte, error) {
	maximumEncoded := base64.RawStdEncoding.EncodedLen(maximumPlaintext+session.aead.Overhead()) + 1
	encoded, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(encoded) > maximumEncoded {
		return nil, errRuntimeFrameTooLarge
	}
	if err != nil || len(encoded) < 2 {
		return nil, errors.New("runtime frame is invalid")
	}
	sealed := make([]byte, base64.RawStdEncoding.DecodedLen(len(encoded)-1))
	decoded, err := base64.RawStdEncoding.Decode(sealed, encoded[:len(encoded)-1])
	if err != nil {
		return nil, errors.New("runtime frame is invalid")
	}
	nonce := make([]byte, session.aead.NonceSize())
	nonce[len(nonce)-1] = direction
	plaintext, err := session.aead.Open(nil, nonce, sealed[:decoded], runtimeFrameAdditionalData(direction))
	if err != nil || len(plaintext) > maximumPlaintext {
		return nil, errors.New("runtime frame is invalid")
	}
	return plaintext, nil
}

func runtimeFrameAdditionalData(direction byte) []byte {
	return append([]byte(runtimeProtocolVersion+"\x00"), direction)
}

func runtimeFrameBufferSize(maximumPlaintext int) int {
	return base64.RawStdEncoding.EncodedLen(maximumPlaintext+16) + 1
}
