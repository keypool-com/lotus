package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/gorilla/websocket"
	"golang.org/x/xerrors"
	"net/http"
	"sync"
	"time"
)

const (
	CommandListWalletRequest int = iota
	CommandListWalletResponse
	CommandSignRequest
	CommandSignResponse
)

type SignRequest struct {
	Signer address.Address
	ToSign []byte
	Meta   api.MsgMeta
}

type SignResponse struct {
	Signature *crypto.Signature
	Err       string
}

type ListWalletResponse struct {
	Addresses []address.Address
	Err       string
}

type command struct {
	Command int
	Data    []byte
}

type clientWallet struct {
	addresses []address.Address
	conn      *websocket.Conn
	close     chan<- bool
}

func (c *clientWallet) call(cmd command) (command, error) {
	err := c.conn.WriteJSON(&cmd)
	if err != nil {
		c.close <- true
		return command{}, xerrors.Errorf("send command, %+v", err)
	}
	var out command
	err = c.conn.ReadJSON(&out)
	if err != nil {
		c.close <- true
	}
	return out, err
}

type WalletServer struct {
	mutex   sync.Mutex
	clients map[*clientWallet]bool
}

func (o *WalletServer) WalletNew(ctx context.Context, keyType types.KeyType) (address.Address, error) {
	return address.Address{}, errors.New("not support to new wallet in a offline wallet")
}

func (o *WalletServer) WalletHas(ctx context.Context, addr address.Address) (bool, error) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	for c := range o.clients {
		for _, a := range c.addresses {
			if a == addr {
				return true, nil
			}
		}
	}
	return false, nil
}

func (o *WalletServer) WalletList(ctx context.Context) ([]address.Address, error) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	addresses := make([]address.Address, 0, 16)
	for c := range o.clients {
		addresses = append(addresses, c.addresses...)
	}
	return addresses, nil
}

func (o *WalletServer) WalletSign(ctx context.Context, signer address.Address, toSign []byte, meta api.MsgMeta) (*crypto.Signature, error) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	for c := range o.clients {
		for _, a := range c.addresses {
			if a == signer {
				req := SignRequest{
					Signer: signer,
					ToSign: toSign,
					Meta:   meta,
				}
				bytes, err := json.Marshal(&req)
				if err != nil {
					return nil, xerrors.Errorf("serialize resquest, %+v", err)
				}
				out, err := c.call(command{
					Command: CommandSignRequest,
					Data:    bytes,
				})
				if err != nil {
					return nil, err
				}
				var resp SignResponse
				if err = json.Unmarshal(out.Data, &resp); err != nil {
					return nil, xerrors.Errorf("deserialize response, %+v", err)
				}
				if len(resp.Err) > 0 {
					return nil, xerrors.New(resp.Err)
				}
				return resp.Signature, nil
			}
		}
	}
	return nil, xerrors.New("wallet not exist")
}

func (o *WalletServer) WalletExport(ctx context.Context, a address.Address) (*types.KeyInfo, error) {
	return nil, errors.New("not support to export a wallet from remote clientWallet")
}

func (o *WalletServer) WalletImport(ctx context.Context, info *types.KeyInfo) (address.Address, error) {
	return address.Address{}, errors.New("not support to import a wallet into remote clientWallet")
}

func (o *WalletServer) WalletDelete(ctx context.Context, a address.Address) error {
	return errors.New("not support to delete a wallet from remote clientWallet")
}

func (o *WalletServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	upgrader := websocket.Upgrader{}
	ws, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		log.Error(err)
	}
	defer ws.Close()
	close := make(chan bool)
	err = o.serve(ws, close)
	if err != nil {
		log.Error(err)
		return
	}
	<-close
}

func (o *WalletServer) serve(c *websocket.Conn, close chan<- bool) error {
	client := &clientWallet{
		addresses: nil,
		conn:      c,
		close:     close,
	}

	out, err := client.call(command{
		Command: CommandListWalletRequest,
		Data:    nil,
	})

	if err != nil {
		return xerrors.Errorf("list wallets, %+v", err)
	}

	if out.Command != CommandListWalletResponse {
		return xerrors.New("expect list wallet response")
	}

	var resp ListWalletResponse

	if err = json.Unmarshal(out.Data, &resp); err != nil {
		return xerrors.Errorf("failed to decode addresses, %+v", err)
	}
	client.addresses = resp.Addresses

	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.clients[client] = true

	c.SetCloseHandler(func(code int, text string) error {
		if code != websocket.CloseNormalClosure {
			log.Errorf("websocket closed, %s", text)
		}
		o.mutex.Lock()
		defer o.mutex.Unlock()
		delete(o.clients, client)
		close <- true
		return nil
	})

	go func() {
		for {
			err := c.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
			if err != nil {
				log.Errorf("send ping: %v+", err)
				close <- true
				break
			}
			time.Sleep(10 * time.Second)
		}
	}()

	return nil
}

func NewWalletServer() *WalletServer {
	return &WalletServer{
		mutex:   sync.Mutex{},
		clients: map[*clientWallet]bool{},
	}
}
