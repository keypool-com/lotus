package main

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/crypto"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
)

type MultiWallet struct {
	wallets []api.WalletAPI
}

func NewMultiWallet() *MultiWallet {
	return &MultiWallet{
		wallets: make([]api.WalletAPI, 0, 10),
	}
}

func (m *MultiWallet) Add(w api.WalletAPI) {
	m.wallets = append(m.wallets, w)
}

func (m *MultiWallet) WalletNew(ctx context.Context, keyType types.KeyType) (address.Address, error) {
	for _, w := range m.wallets {
		a, err := w.WalletNew(ctx, keyType)
		if err == nil {
			return a, err
		}
	}

	return address.Undef, xerrors.Errorf("no wallet backends supporting key type: %s", keyType)
}

func (m *MultiWallet) WalletHas(ctx context.Context, address address.Address) (bool, error) {
	for _, w := range m.wallets {
		a, err := w.WalletHas(ctx, address)
		if err != nil {
			log.Fatalf("wallet has: %+v", err)
		}
		if a {
			return true, nil
		}
	}
	return false, nil
}

func (m *MultiWallet) WalletList(ctx context.Context) ([]address.Address, error) {
	out := make([]address.Address, 0)
	seen := map[address.Address]struct{}{}

	for _, w := range m.wallets {
		l, err := w.WalletList(ctx)
		if err != nil {
			return nil, err
		}

		for _, a := range l {
			if _, ok := seen[a]; ok {
				continue
			}
			seen[a] = struct{}{}

			out = append(out, a)
		}
	}

	return out, nil
}

func (m *MultiWallet) WalletSign(ctx context.Context, signer address.Address, toSign []byte, meta api.MsgMeta) (*crypto.Signature, error) {
	for _, w := range m.wallets {
		a, err := w.WalletHas(ctx, signer)
		if err != nil {
			log.Fatalf("wallet has: %+v", err)
		}
		if a {
			return w.WalletSign(ctx, signer, toSign, meta)
		}
	}

	return nil, xerrors.Errorf("key not found")
}

func (m *MultiWallet) WalletExport(ctx context.Context, address address.Address) (*types.KeyInfo, error) {
	for _, w := range m.wallets {
		a, err := w.WalletHas(ctx, address)
		if err != nil {
			log.Fatalf("wallet has: %+v", err)
		}
		if a {
			return w.WalletExport(ctx, address)
		}
	}

	return nil, xerrors.Errorf("key not found")
}

func (m *MultiWallet) WalletImport(ctx context.Context, info *types.KeyInfo) (address.Address, error) {
	for _, w := range m.wallets {
		a, err := w.WalletImport(ctx, info)
		if err == nil {
			return a, err
		}
	}

	return address.Undef, xerrors.Errorf("no wallet backends supporting key type: %s", info.Type)
}

func (m *MultiWallet) WalletDelete(ctx context.Context, address address.Address) error {
	for _, w := range m.wallets {
		a, err := w.WalletHas(ctx, address)
		if err != nil {
			log.Fatalf("wallet has: %+v", err)
		}
		if a {
			return w.WalletDelete(ctx, address)
		}
	}
	return xerrors.Errorf("key not found")
}
