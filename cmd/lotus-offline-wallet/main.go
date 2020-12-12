package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/wallet"
	ledgerwallet "github.com/filecoin-project/lotus/chain/wallet/ledger"
	"github.com/ipfs/go-cid"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/mux"
	logging "github.com/ipfs/go-log/v2"
	"github.com/urfave/cli/v2"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	"github.com/filecoin-project/go-jsonrpc"

	"github.com/filecoin-project/lotus/build"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/filecoin-project/lotus/lib/lotuslog"
	"github.com/filecoin-project/lotus/metrics"
	"github.com/filecoin-project/lotus/node/repo"
)

var log = logging.Logger("main")

const FlagWalletRepo = "wallet-repo"

func main() {
	lotuslog.SetupLogLevels()

	local := []*cli.Command{
		runCmd,
		walletCmd,
		nodeCmd,
		signCmd,
	}

	app := &cli.App{
		Name:    "lotus-offline-wallet",
		Usage:   "Basic external wallet",
		Version: build.UserVersion(),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    FlagWalletRepo,
				EnvVars: []string{"WALLET_PATH"},
				Value:   "~/.lotuswallet", // TODO: Consider XDG_DATA_HOME
			},
		},

		Commands: local,
	}
	app.Setup()

	if err := app.Run(os.Args); err != nil {
		log.Warnf("%+v", err)
		return
	}
}

var runCmd = &cli.Command{
	Name:  "run",
	Usage: "Start lotus wallet",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "listen",
			Usage: "host address and port the wallet api will listen on",
			Value: "0.0.0.0:1777",
		},
		&cli.BoolFlag{
			Name:  "ledger",
			Usage: "use a ledger device instead of an on-disk wallet",
		},
	},
	Action: func(cctx *cli.Context) error {
		log.Info("Starting lotus wallet")

		ctx := lcli.ReqContext(cctx)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Register all metric views
		if err := view.Register(
			metrics.DefaultViews...,
		); err != nil {
			log.Fatalf("Cannot register the view: %v", err)
		}

		repoPath := cctx.String(FlagWalletRepo)
		r, err := repo.NewFS(repoPath)
		if err != nil {
			return err
		}

		ok, err := r.Exists()
		if err != nil {
			return err
		}
		if !ok {
			if err := r.Init(repo.Worker); err != nil {
				return err
			}
		}

		lr, err := r.Lock(repo.Wallet)
		if err != nil {
			return err
		}

		ds, err := lr.Datastore("/metadata")
		if err != nil {
			return nil
		}

		ow := &OfflineWallet{
			ds: ds,
			unsigned: make(map[address.Address]map[cid.Cid]PendingUnsignedMessage),
			lock:     sync.Mutex{},
		}

		address := cctx.String("listen")
		mux := mux.NewRouter()

		log.Info("Setting up API endpoint at " + address)

		rpcServer := jsonrpc.NewServer()
		rpcServer.Register("Filecoin", ow)

		mux.Handle("/rpc/v0", rpcServer)
		mux.PathPrefix("/").Handler(http.DefaultServeMux) // pprof

		srv := &http.Server{
			Handler: mux,
			BaseContext: func(listener net.Listener) context.Context {
				ctx, _ := tag.New(context.Background(), tag.Upsert(metrics.APIInterface, "lotus-wallet"))
				return ctx
			},
		}

		go func() {
			<-ctx.Done()
			log.Warn("Shutting down...")
			if err := srv.Shutdown(context.TODO()); err != nil {
				log.Errorf("shutting down RPC server failed: %s", err)
			}
			log.Warn("Graceful shutdown successful")
		}()

		nl, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}

		return srv.Serve(nl)
	},
}

var signCmd = &cli.Command{
	Name:  "sign",
	Usage: "sign transaction-hex",
	Action: func(cctx *cli.Context) error {
		if cctx.NArg() != 1 {
			return fmt.Errorf("require transaction hex")
		}

		data, err := hex.DecodeString(cctx.Args().First())
		if err != nil {
			return nil
		}

		buf := bytes.NewBuffer(data)

		var msg UnsignedMessage
		err = msg.UnmarshalCBOR(buf)
		if err != nil {
			return err
		}

		w, err := createWallet(cctx)
		if err != nil {
			return err
		}

		ctx := lcli.ReqContext(cctx)

		sig, err := w.WalletSign(ctx, msg.Address, msg.ToSign, api.MsgMeta{
			Type: msg.Meta.Type,
			Extra: msg.Meta.Extra,
		})

		if err != nil {
			return err
		}

		signed := SignedMessage{
			UnsignedMessage: msg,
			Signature: *sig,
		}

		buf.Reset()

		err = signed.MarshalCBOR(buf)
		if err != nil {
			return err
		}

		fmt.Printf("%s\n", hex.EncodeToString(buf.Bytes()))

		return nil
	},
}

func apiClient(cctx *cli.Context) (*client, error) {
	server := cctx.String("server")
	ctx := lcli.ReqContext(cctx)

	c := &client{}
	if _, err := jsonrpc.NewClient(ctx, "ws://"+server+"/rpc/v0", "Filecoin", c, nil); err != nil {
		return nil, err
	}
	return c, nil
}

func createWallet(cctx *cli.Context) (api.WalletAPI, error) {
	repoPath := cctx.String(FlagWalletRepo)
	r, err := repo.NewFS(repoPath)
	if err != nil {
		return nil, err
	}

	ok, err := r.Exists()
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := r.Init(repo.Worker); err != nil {
			return nil, err
		}
	}

	lr, err := r.Lock(repo.Wallet)
	if err != nil {
		return nil, err
	}

	ks, err := lr.KeyStore()
	if err != nil {
		return nil, err
	}

	lw, err := wallet.NewWallet(ks)
	if err != nil {
		return nil, err
	}

	var w api.WalletAPI = lw
	ds, err := lr.Datastore("/metadata")
	if err != nil {
		return nil, err
	}

	w = wallet.MultiWallet{
		Local:  lw,
		Ledger: ledgerwallet.NewWallet(ds),
	}
	return w, nil
}
