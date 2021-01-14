package tripledh

import (
	"fmt"

	"github.com/bytemare/cryptotools/encoding"
	"github.com/bytemare/cryptotools/utils"
	"github.com/bytemare/opaque/internal"
)

func serverK3dh(core *internal.Core, sk, epku, pku []byte) ([]byte, error) {
	sks, err := core.NewScalar().Decode(sk)
	if err != nil {
		return nil, fmt.Errorf("sk : %w", err)
	}

	epk, err := core.NewElement().Decode(epku)
	if err != nil {
		return nil, fmt.Errorf("epku : %w", err)
	}

	gpk, err := core.NewElement().Decode(pku)
	if err != nil {
		return nil, fmt.Errorf("pku : %w", err)
	}

	e1 := epk.Mult(core.Esk)
	e2 := epk.Mult(sks)
	e3 := gpk.Mult(core.Esk)

	return utils.Concatenate(0, e1.Bytes(), e2.Bytes(), e3.Bytes()), nil
}

func Response(core *internal.Core, m *internal.Metadata, nonceLen int, sk, pku, req, info2 []byte, enc encoding.Encoding) (encKe2, einfo2 []byte, err error) {
	ke1, err := internal.DecodeKe1(req, enc)
	if err != nil {
		return nil, nil, err
	}

	core.Esk = core.NewScalar().Random()
	core.Epk = core.Base().Mult(core.Esk)
	core.NonceS = utils.RandomBytes(nonceLen)

	ikm, err := serverK3dh(core, sk, ke1.EpkU, pku)
	if err != nil {
		return nil, nil, err
	}

	core.DeriveKeys(m, tag3DH, ke1.NonceU, core.NonceS, ikm)

	if info2 != nil {
		einfo2, err = internal.AesGcmDecrypt(core.Ke2, info2)
		if err != nil {
			return nil, nil, err
		}
	}

	core.Transcript2 = utils.Concatenate(0, m.CredReq, ke1.NonceU, m.Info1, ke1.EpkU, m.CredResp, core.NonceS, info2, core.Epk.Bytes(), einfo2)

	return ke2{
		NonceS: core.NonceS,
		EpkS:   core.Epk.Bytes(),
		Mac:    core.Hmac(core.Transcript2, core.Km2),
	}.Encode(enc), einfo2, nil
}

func ServerFinalize(core *internal.Core, req []byte, enc encoding.Encoding) error {
	ke3, err := decodeKe3(req, enc)
	if err != nil {
		return err
	}

	core.Transcript3 = utils.Concatenate(0, core.Transcript2)

	if !checkHmac(core.Hash, core.Transcript3, core.Km3, ke3.Mac) {
		return internal.ErrAkeInvalidClientMac
	}

	return nil
}