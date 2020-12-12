package main

import (
	"context"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
)

type client struct {
	WalletNew func(context.Context, types.KeyType) (address.Address, error)
	WalletHas func(context.Context, address.Address) (bool, error)
	WalletList func(context.Context) ([]address.Address, error)
	WalletSign func(ctx context.Context, signer address.Address, toSign []byte, meta api.MsgMeta) (*crypto.Signature, error)
	WalletExport func(context.Context, address.Address) (*types.KeyInfo, error)
	WalletImport func(context.Context, *types.KeyInfo) (address.Address, error)
	WalletDelete func(context.Context, address.Address) error
	GetPendingMessages func(ctx context.Context) ([]*UnsignedMessage, error)
	SubmitSignature func(ctx context.Context, message *SignedMessage) error
}