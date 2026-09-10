package main

import (
	"crypto/ed25519"
	"encoding/base64"
)

const ccxSignerPublicKeyB64 = "QT3eo2NLcKY9s2EJUptkv2d5AAVGK/dJgR0NRpGedEs="

func signerPublicKey() ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(ccxSignerPublicKeyB64)
	if err != nil {
		return nil
	}
	return ed25519.PublicKey(b)
}
