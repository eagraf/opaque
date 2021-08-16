// SPDX-License-Identifier: MIT
//
// Copyright (C) 2021 Daniel Bourdrez. All Rights Reserved.
//
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree or at
// https://spdx.org/licenses/MIT.html

// Package internal provides structures and functions to operate OPAQUE that are not part of the public API.
package internal

import (
	cryptorand "crypto/rand"
	"errors"
	"fmt"

	"github.com/bytemare/crypto/group"

	"github.com/bytemare/opaque/internal/encoding"
	cred "github.com/bytemare/opaque/internal/message"
	"github.com/bytemare/opaque/internal/oprf"
	"github.com/bytemare/opaque/internal/tag"
	"github.com/bytemare/opaque/message"
)

// NonceLength is the default length used for nonces.
const NonceLength = 32

var errInvalidMessageLength = errors.New("invalid message length")

// RandomBytes returns random bytes of length len (wrapper for crypto/rand).
func RandomBytes(length int) []byte {
	r := make([]byte, length)
	if _, err := cryptorand.Read(r); err != nil {
		// We can as well not panic and try again in a loop and a counter to stop.
		panic(fmt.Errorf("unexpected error in generating random bytes : %w", err))
	}

	return r
}

// Parameters is the internal representation of the instance runtime parameters.
type Parameters struct {
	KDF             *KDF
	MAC             *Mac
	Hash            *Hash
	MHF             *MHF
	NonceLen        int
	EnvelopeSize    int
	OPRFPointLength int
	AkePointLength  int
	Group           group.Group
	OPRF            oprf.Ciphersuite
	Context         []byte
}

// DeserializeRegistrationRequest takes a serialized RegistrationRequest message as input and attempts to deserialize it.
func (p *Parameters) DeserializeRegistrationRequest(input []byte) (*message.RegistrationRequest, error) {
	if len(input) != p.OPRFPointLength {
		return nil, errInvalidMessageLength
	}

	return &message.RegistrationRequest{Data: input}, nil
}

// DeserializeRegistrationResponse takes a serialized RegistrationResponse message as input and attempts to deserialize it.
func (p *Parameters) DeserializeRegistrationResponse(input []byte) (*message.RegistrationResponse, error) {
	if len(input) != p.OPRFPointLength+p.AkePointLength {
		return nil, errInvalidMessageLength
	}

	return &message.RegistrationResponse{
		Data: input[:p.OPRFPointLength],
		Pks:  input[p.OPRFPointLength:],
	}, nil
}

// DeserializeRegistrationRecord takes a serialized RegistrationRecord message as input and attempts to deserialize it.
func (p *Parameters) DeserializeRegistrationRecord(input []byte) (*message.RegistrationRecord, error) {
	if len(input) != p.AkePointLength+p.Hash.Size()+p.EnvelopeSize {
		return nil, errInvalidMessageLength
	}

	pku := input[:p.AkePointLength]
	maskingKey := input[p.AkePointLength : p.AkePointLength+p.Hash.Size()]
	env := input[p.AkePointLength+p.Hash.Size():]

	return &message.RegistrationRecord{
		PublicKey:  pku,
		MaskingKey: maskingKey,
		Envelope:   env,
	}, nil
}

func (p *Parameters) deserializeCredentialRequest(input []byte) *cred.CredentialRequest {
	return &cred.CredentialRequest{Data: input[:p.OPRFPointLength]}
}

func (p *Parameters) deserializeCredentialResponse(input []byte, maxResponseLength int) *cred.CredentialResponse {
	return &cred.CredentialResponse{
		Data:           input[:p.OPRFPointLength],
		MaskingNonce:   input[p.OPRFPointLength : p.OPRFPointLength+p.NonceLen],
		MaskedResponse: input[p.OPRFPointLength+p.NonceLen : maxResponseLength],
	}
}

// DeserializeKE1 takes a serialized KE1 message as input and attempts to deserialize it.
func (p *Parameters) DeserializeKE1(input []byte) (*message.KE1, error) {
	if len(input) != p.OPRFPointLength+p.NonceLen+p.AkePointLength {
		return nil, errInvalidMessageLength
	}

	creq := p.deserializeCredentialRequest(input[:p.OPRFPointLength])
	nonceU := input[p.OPRFPointLength : p.OPRFPointLength+p.NonceLen]

	return &message.KE1{
		CredentialRequest: creq,
		NonceU:            nonceU,
		EpkU:              input[p.OPRFPointLength+p.NonceLen:],
	}, nil
}

// DeserializeKE2 takes a serialized KE2 message as input and attempts to deserialize it.
func (p *Parameters) DeserializeKE2(input []byte) (*message.KE2, error) {
	maxResponseLength := p.OPRFPointLength + p.NonceLen + p.AkePointLength + p.EnvelopeSize

	if len(input) != maxResponseLength+p.NonceLen+p.AkePointLength+p.MAC.Size() {
		return nil, errInvalidMessageLength
	}

	cresp := p.deserializeCredentialResponse(input, maxResponseLength)

	nonceS := input[maxResponseLength : maxResponseLength+p.NonceLen]
	offset := maxResponseLength + p.NonceLen
	epks := input[offset : offset+p.AkePointLength]
	offset += p.AkePointLength
	mac := input[offset:]

	return &message.KE2{
		CredentialResponse: cresp,
		NonceS:             nonceS,
		EpkS:               epks,
		Mac:                mac,
	}, nil
}

// DeserializeKE3 takes a serialized KE3 message as input and attempts to deserialize it.
func (p *Parameters) DeserializeKE3(input []byte) (*message.KE3, error) {
	if len(input) != p.MAC.Size() {
		return nil, errInvalidMessageLength
	}

	return &message.KE3{Mac: input}, nil
}

// MaskResponse is used to encrypt and decrypt the response in KE2.
func (p *Parameters) MaskResponse(key, nonce, in []byte) []byte {
	pad := p.KDF.Expand(key, encoding.SuffixString(nonce, tag.CredentialResponsePad), encoding.PointLength[p.Group]+p.EnvelopeSize)
	return Xor(pad, in)
}