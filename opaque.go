// SPDX-License-Identifier: MIT
//
// Copyright (C) 2021 Daniel Bourdrez. All Rights Reserved.
//
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree or at
// https://spdx.org/licenses/MIT.html

// Package opaque implements the OPAQUE asymmetric password-authenticated key exchange protocol.
//
// OPAQUE is an asymmetric Password Authenticated Key Exchange (PAKE).
//
// This package implements the official OPAQUE definition. For protocol details, please refer to the IETF protocol
// document at https://datatracker.ietf.org/doc/draft-irtf-cfrg-opaque.
//
package opaque

import (
	"crypto"
	"errors"
	"fmt"

	"github.com/bytemare/crypto/group"
	"github.com/bytemare/crypto/hash"
	"github.com/bytemare/crypto/ksf"

	"github.com/bytemare/opaque/internal"
	"github.com/bytemare/opaque/internal/encoding"
	"github.com/bytemare/opaque/internal/oprf"
	"github.com/bytemare/opaque/message"
)

// Group identifies the prime-order group with hash-to-curve capability to use in OPRF and AKE.
type Group byte

const (
	// RistrettoSha512 identifies the Ristretto255 group and SHA-512.
	RistrettoSha512 = Group(oprf.RistrettoSha512)

	// decaf448Shake256 identifies the Decaf448 group and Shake-256.
	// decaf448Shake256 = 2.

	// P256Sha256 identifies the NIST P-256 group and SHA-256.
	P256Sha256 = Group(oprf.P256Sha256)

	// P384Sha512 identifies the NIST P-384 group and SHA-384.
	P384Sha512 = Group(oprf.P384Sha384)

	// P521Sha512 identifies the NIST P-512 group and SHA-512.
	P521Sha512 = Group(oprf.P521Sha512)

	// Curve25519Sha512 identifies a group over Curve25519 with SHA2-512 hash-to-group hashing.
	// Curve25519Sha512 = Group(group.Curve25519Sha512).

	confLength = 6
)

var (
	errInvalidKDFid  = errors.New("invalid KDF id")
	errInvalidMACid  = errors.New("invalid MAC id")
	errInvalidHASHid = errors.New("invalid Hash id")
	errInvalidKSFid  = errors.New("invalid KSF id")
	errInvalidOPRFid = errors.New("invalid OPRF group id")
	errInvalidAKEid  = errors.New("invalid AKE group id")
)

// Credentials holds the client and server ids (will certainly disappear in next versions°.
type Credentials struct {
	Client, Server              []byte
	TestEnvNonce, TestMaskNonce []byte
}

// Configuration represents an OPAQUE configuration. Note that OprfGroup and AKEGroup are recommended to be the same,
// as well as KDF, MAC, Hash should be the same.
type Configuration struct {
	// Context is optional shared information to include in the AKE transcript.
	Context []byte

	// KDF identifies the hash function to be used for key derivation (e.g. HKDF).
	KDF crypto.Hash `json:"kdf"`

	// MAC identifies the hash function to be used for message authentication (e.g. HMAC).
	MAC crypto.Hash `json:"mac"`

	// Hash identifies the hash function to be used for hashing, as defined in github.com/bytemare/crypto/hash.
	Hash crypto.Hash `json:"hash"`

	// OPRF identifies the ciphersuite to use for the OPRF.
	OPRF Group `json:"oprf"`

	// KSF identifies the key stretching function for expensive key derivation on the client,
	// defined in github.com/bytemare/crypto/ksf.
	KSF ksf.Identifier `json:"ksf"`

	// AKE identifies the group to use for the AKE.
	AKE Group `json:"group"`
}

// Client returns a newly instantiated Client from the Configuration.
func (c *Configuration) Client() (*Client, error) {
	return NewClient(c)
}

// Server returns a newly instantiated Server from the Configuration.
func (c *Configuration) Server() (*Server, error) {
	return NewServer(c)
}

// verify returns an error on the first non-compliant parameter, ni otherwise.
func (c *Configuration) verify() error {
	if !hash.Hashing(c.KDF).Available() {
		return errInvalidKDFid
	}

	if !hash.Hashing(c.MAC).Available() {
		return errInvalidMACid
	}

	if !hash.Hashing(c.Hash).Available() {
		return errInvalidHASHid
	}

	if c.KSF != 0 && !c.KSF.Available() {
		return errInvalidKSFid
	}

	if !oprf.Ciphersuite(c.OPRF).Available() {
		return errInvalidOPRFid
	}

	if !group.Group(c.AKE).Available() {
		return errInvalidAKEid
	}

	return nil
}

// toInternal builds the internal representation of the configuration parameters.
func (c *Configuration) toInternal() (*internal.Parameters, error) {
	if err := c.verify(); err != nil {
		return nil, err
	}

	g := group.Group(c.AKE)
	ip := &internal.Parameters{
		KDF:             internal.NewKDF(c.KDF),
		MAC:             internal.NewMac(c.MAC),
		Hash:            internal.NewHash(c.Hash),
		KSF:             internal.NewKSF(c.KSF),
		NonceLen:        internal.NonceLength,
		OPRFPointLength: encoding.PointLength[group.Group(c.OPRF)],
		AkePointLength:  encoding.PointLength[g],
		Group:           g,
		OPRF:            oprf.Ciphersuite(c.OPRF),
		Context:         c.Context,
	}
	ip.EnvelopeSize = ip.NonceLen + ip.MAC.Size()

	return ip, nil
}

// Serialize returns the byte encoding of the Configuration structure.
func (c *Configuration) Serialize() []byte {
	b := []byte{
		byte(c.OPRF),
		byte(c.KDF),
		byte(c.MAC),
		byte(c.Hash),
		byte(c.KSF),
		byte(c.AKE),
	}

	return encoding.Concat(b, encoding.EncodeVector(c.Context))
}

// DeserializeConfiguration decodes the input and returns a Parameter structure. This assumes that the encoded parameters
// are valid, and will not be checked.
func DeserializeConfiguration(encoded []byte) (*Configuration, error) {
	if len(encoded) < confLength+2 { // corresponds to the configuration length + 2-byte encoding of empty context
		return nil, internal.ErrConfigurationInvalidLength
	}

	ctx, _, err := encoding.DecodeVector(encoded[confLength:])
	if err != nil {
		return nil, fmt.Errorf("decoding the configuration context: %w", err)
	}

	return &Configuration{
		OPRF:    Group(encoded[0]),
		KDF:     crypto.Hash(encoded[1]),
		MAC:     crypto.Hash(encoded[2]),
		Hash:    crypto.Hash(encoded[3]),
		KSF:     ksf.Identifier(encoded[4]),
		AKE:     Group(encoded[5]),
		Context: ctx,
	}, nil
}

// DefaultConfiguration returns a default configuration with strong parameters.
func DefaultConfiguration() *Configuration {
	return &Configuration{
		OPRF:    RistrettoSha512,
		KDF:     crypto.SHA512,
		MAC:     crypto.SHA512,
		Hash:    crypto.SHA512,
		KSF:     ksf.Scrypt,
		AKE:     RistrettoSha512,
		Context: nil,
	}
}

// ClientRecord is a server-side structure enabling the storage of user relevant information.
type ClientRecord struct {
	CredentialIdentifier []byte
	ClientIdentity       []byte
	*message.RegistrationRecord

	// testing
	TestMaskNonce []byte
}

// GetFakeEnvelope returns a byte array filled with 0s the length of a legitimate envelope size in the configuration's.
// This fake envelope byte array is used in the client enumeration mitigation scheme.
func GetFakeEnvelope(c *Configuration) []byte {
	if !hash.Hashing(c.MAC).Available() {
		panic(errInvalidMACid)
	}

	envelopeSize := internal.NonceLength + internal.NewMac(c.MAC).Size()

	return make([]byte, envelopeSize)
}
