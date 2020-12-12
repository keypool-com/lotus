package main

import (
	"context"
	"fmt"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/lib/sigs"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	"sync"
	"time"
)

const dsOfflinePrefix = "/offlinekey/"

type OfflineWalletAPI interface {
	GetPendingMessages(ctx context.Context) ([]*UnsignedMessage, error)
	SubmitSignature(ctx context.Context, message *SignedMessage) error
}

type Meta struct {
	Type api.MsgType
	Extra []byte
}

type UnsignedMessage struct {
	Address address.Address
	ToSign []byte
	Meta Meta
}

type SignedMessage struct {
	UnsignedMessage
	crypto.Signature
}

type PendingUnsignedMessage struct {
	msg *UnsignedMessage
	ch chan *crypto.Signature
}

type OfflineWallet struct {
	ds datastore.Batching
	unsigned map[address.Address]map[cid.Cid]PendingUnsignedMessage
	lock sync.Mutex
}

func (o *OfflineWallet) WalletNew(ctx context.Context, keyType types.KeyType) (address.Address, error) {
	return address.Undef, fmt.Errorf("cannot new keys from offline wallets, please use import instead")
}

func (o *OfflineWallet) WalletHas(ctx context.Context, address address.Address) (bool, error) {
	_, err := o.ds.Get(keyForAddr(address))
	if err == nil {
		return true, nil
	}
	if err == datastore.ErrNotFound {
		return false, nil
	}
	return false, err
}

func (o *OfflineWallet) WalletList(ctx context.Context) ([]address.Address, error) {
	res, err := o.ds.Query(query.Query{Prefix: dsOfflinePrefix})
	if err != nil {
		return nil, err
	}
	defer res.Close() // nolint:errcheck

	var out []address.Address
	for {
		res, ok := res.NextSync()
		if !ok {
			break
		}

		addr, err := address.NewFromBytes(res.Value)
		if err != nil {
			return nil, err
		}

		out = append(out, addr)
	}
	return out, nil
}

func (o *OfflineWallet) WalletSign(ctx context.Context, signer address.Address, toSign []byte, meta api.MsgMeta) (*crypto.Signature, error) {
	_, c, err := cid.CidFromBytes(toSign)
	if err != nil {
		return nil, err
	}

	if meta.Type != api.MTChainMsg {
		return nil, fmt.Errorf("only MTChainMsg is supported")
	}

	ch := func() chan *crypto.Signature {
		o.lock.Lock()
		defer o.lock.Unlock()

		if o.unsigned[signer] == nil {
			o.unsigned[signer] = make(map[cid.Cid]PendingUnsignedMessage)
		}
		ch := make(chan *crypto.Signature)

		o.unsigned[signer][c] = PendingUnsignedMessage{
			msg: &UnsignedMessage{
				Address: signer,
				ToSign: toSign,
				Meta: Meta{
					Type:  meta.Type,
					Extra: meta.Extra,
				},
			},
			ch:  ch,
		}
		return ch
	}()

	defer func() {
		o.lock.Lock()
		defer o.lock.Unlock()

		if o.unsigned[signer] != nil {
			delete(o.unsigned[signer], c)
		}
	}()

	select {
	case <- time.After(5*time.Minute):
		return nil, fmt.Errorf("timeout to wait to submit signature")
	case sig := <-ch:
		return sig, nil
	}
}

func (o *OfflineWallet) WalletExport(ctx context.Context, address address.Address) (*types.KeyInfo, error) {
	return nil, fmt.Errorf("cannot export keys from offline wallets")
}

func (o *OfflineWallet) WalletImport(ctx context.Context, info *types.KeyInfo) (address.Address, error) {
	addr, err := address.NewFromString(string(info.PrivateKey))

	if err != nil {
		return addr, err
	}

	if addr.Protocol() != address.SECP256K1 && addr.Protocol() != address.BLS {
		return addr, fmt.Errorf("only SECP256K1 or BLS keys are supported")
	}

	return addr, o.ds.Put(keyForAddr(addr), addr.Bytes())
}

func (o *OfflineWallet) WalletDelete(ctx context.Context, address address.Address) error {
	return o.ds.Delete(keyForAddr(address))
}

func (o *OfflineWallet) GetPendingMessages(ctx context.Context) ([]*UnsignedMessage, error) {
	o.lock.Lock()
	defer o.lock.Unlock()
	unsigned := make([]*UnsignedMessage, 0)
	for _, p := range o.unsigned {
		for _, m := range p {
			unsigned = append(unsigned, m.msg)
		}
	}

	return unsigned, nil
}

func (o *OfflineWallet) SubmitSignature(ctx context.Context, message *SignedMessage) error {
	_, cid, err := cid.CidFromBytes(message.ToSign)
	if err != nil {
		return err
	}

	err = sigs.Verify(&message.Signature, message.Address, message.ToSign)
	if err != nil {
		return err
	}

	o.lock.Lock()
	defer o.lock.Unlock()
	for _, p := range o.unsigned {
		for c, m := range p {
			if c == cid {
				m.ch <- &message.Signature
				return nil
			}
		}
	}
	return fmt.Errorf("message %v does not exist", cid)
}

func keyForAddr(addr address.Address) datastore.Key {
	return datastore.NewKey(dsOfflinePrefix + addr.String())
}
