package main

import (
	"context"
	"errors"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
)

type SafeWallet struct {
	api.WalletAPI
}

func (s *SafeWallet) WalletNew(ctx context.Context, keyType types.KeyType) (address.Address, error) {
	return address.Address{}, errors.New("safe wallet is enabled, use import instead of new")
}

func (s *SafeWallet) WalletHas(ctx context.Context, address address.Address) (bool, error) {
	return s.WalletAPI.WalletHas(ctx, address)
}

func (s SafeWallet) WalletList(ctx context.Context) ([]address.Address, error) {
	return s.WalletAPI.WalletList(ctx)
}

func (s *SafeWallet) WalletSign(ctx context.Context, signer address.Address, toSign []byte, meta api.MsgMeta) (*crypto.Signature, error) {
	return s.WalletAPI.WalletSign(ctx, signer, toSign, meta)
}

func (s *SafeWallet) WalletExport(ctx context.Context, address address.Address) (*types.KeyInfo, error) {
	return nil, errors.New("DO NOT EXPORT THIS WALLET")
}

func (s *SafeWallet) WalletImport(ctx context.Context, info *types.KeyInfo) (address.Address, error) {
	return s.WalletAPI.WalletImport(ctx, info)
}

func (s *SafeWallet) WalletDelete(ctx context.Context, address address.Address) error {
	return s.WalletAPI.WalletDelete(ctx, address)
}
