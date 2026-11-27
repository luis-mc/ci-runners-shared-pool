package tlsca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// ErrBadSignature is a signature that does not match its message and chain.
var ErrBadSignature = errors.New("the signature does not match")

// Sign signs msg with the server key. It returns the signature and the chain
// (DER, server certificate first) the agent checks against its pin. Used by
// the frozen recovery call (operations R5.44): the agent trusts what it
// pinned, not the transport alone.
func (a *Authority) Sign(msg []byte) (sig []byte, chain [][]byte, err error) {
	key, ok := a.server.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, nil, errors.New("tlsca: the server key cannot sign")
	}
	sum := sha256.Sum256(msg)
	sig, err = key.Sign(rand.Reader, sum[:], crypto.SHA256)
	if err != nil {
		return nil, nil, fmt.Errorf("tlsca: %w", err)
	}
	return sig, append([][]byte(nil), a.server.Certificate...), nil
}

// VerifySigned checks that chain leads to the pin (as VerifyPin) and that its
// server certificate made sig over msg.
func VerifySigned(pin string, chain [][]byte, msg, sig []byte, now time.Time) error {
	if err := VerifyPin(pin, chain, now); err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return ErrBadSignature
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return ErrBadSignature
	}
	sum := sha256.Sum256(msg)
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return ErrBadSignature
	}
	return nil
}
