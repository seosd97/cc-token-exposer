package main

import (
	"crypto/ed25519"
	"encoding/base64"
)

// ccxSignerPublicKeyB64 is the base64 ed25519 public key that signs release
// checksums.txt files. ccx update verifies the checksums signature against this
// key before installing anything (see internal/selfupdate.VerifySignedChecksums
// and the key handoff note in AGENT.md).
const ccxSignerPublicKeyB64 = "QT3eo2NLcKY9s2EJUptkv2d5AAVGK/dJgR0NRpGedEs="

func signerPublicKey() ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(ccxSignerPublicKeyB64)
	if err != nil {
		return nil
	}
	return ed25519.PublicKey(b)
}
