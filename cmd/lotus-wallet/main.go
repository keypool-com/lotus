package main

import (
	"context"
	"encoding/json"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/filecoin-project/lotus/api/apistruct"
	"github.com/filecoin-project/lotus/chain/wallet/remotewallet"
	cliutil "github.com/filecoin-project/lotus/cli/util"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	logging "github.com/ipfs/go-log/v2"
	"github.com/urfave/cli/v2"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"golang.org/x/xerrors"
	"net"
	"net/http"
	"os"
	"os/signal"

	"github.com/filecoin-project/go-jsonrpc"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/chain/wallet"
	ledgerwallet "github.com/filecoin-project/lotus/chain/wallet/ledger"
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
		connectCmd,
	}

	app := &cli.App{
		Name:    "lotus-wallet",
		Usage:   "Basic external wallet",
		Version: build.UserVersion(),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    FlagWalletRepo,
				EnvVars: []string{"WALLET_PATH"},
				Value:   "~/.lotuswallet", // TODO: Consider XDG_DATA_HOME
			},
			&cli.StringFlag{
				Name:    "repo",
				EnvVars: []string{"LOTUS_PATH"},
				Hidden:  true,
				Value:   "~/.lotus",
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
		&cli.BoolFlag{
			Name:  "safe",
			Usage: "enable safe wallet, new and export are disabled",
		},
		&cli.StringFlag{
			Name:  "remote",
			Usage: "set remote wallet address to build a wallet chain",
		},
		&cli.BoolFlag{
			Name:  "server",
			Usage: "enable wallet server which can accept connection from remote wallet clientWallet",
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

		ks, err := lr.KeyStore()
		if err != nil {
			return err
		}

		var lw api.WalletAPI
		lw, err = wallet.NewWallet(ks)
		if err != nil {
			return err
		}

		if cctx.IsSet("safe") {
			lw = &SafeWallet{lw}
		}

		multi := NewMultiWallet()
		multi.Add(lw)

		if cctx.Bool("ledger") {
			ds, err := lr.Datastore(context.Background(), "/metadata")
			if err != nil {
				return err
			}

			ledger := ledgerwallet.NewWallet(ds)
			multi.Add(ledger)
		}

		server := cctx.Bool("server")
		var walletServer *WalletServer
		if server {
			walletServer = NewWalletServer()
			multi.Add(walletServer)
		}

		if cctx.IsSet("remote") {
			rw, closer, err := newRemoteWallet(cctx.Context, cctx.String("remote"))
			if err != nil {
				return err
			}
			defer closer()
			multi.Add(rw)
		}

		address := cctx.String("listen")
		mux := mux.NewRouter()

		log.Info("Setting up API endpoint at " + address)

		w := &LoggedWallet{under: multi}

		rpcServer := jsonrpc.NewServer()
		rpcServer.Register("Filecoin", metrics.MetricedWalletAPI(w))

		mux.Handle("/rpc/v0", rpcServer)
		if server {
			mux.Handle("/ws", walletServer)
		}
		mux.PathPrefix("/").Handler(http.DefaultServeMux) // pprof

		/*ah := &auth.Handler{
			Verify: nodeApi.AuthVerify,
			Next:   mux.ServeHTTP,
		}*/

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

var connectCmd = &cli.Command{
	Name:      "connect",
	ArgsUsage: "server (default ws://localhost:1777)",
	Usage:     "Connect to lotus wallet server",
	Action: func(cctx *cli.Context) error {
		var server string
		if cctx.NArg() == 0 {
			server = "ws://localhost:1777"
		} else {
			server = cctx.Args().Get(0)
		}

		w, err := createWallet(cctx)
		if err != nil {
			return err
		}

		log.Infof("Connecting to %s", server)
		c, _, err := websocket.DefaultDialer.Dial(server+"/ws", nil)
		if err != nil {
			return xerrors.Errorf("dial:", err)
		}
		defer c.Close()

		log.Info("connected")

		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)

		done := make(chan struct{})

		go func() {
			defer close(done)
			for {
				var out command
				err := c.ReadJSON(&out)
				if err != nil {
					log.Fatalf("read: %+v", err)
					return
				}

				var resp command
				switch out.Command {
				case CommandListWalletRequest:
					resp.Command = CommandListWalletResponse
					addrs, err := w.WalletList(cctx.Context)
					var e string
					if err != nil {
						e = err.Error()
					}
					resp.Data, err = json.Marshal(
						&ListWalletResponse{
							Addresses: addrs,
							Err:       e,
						})
					if err != nil {
						panic(err)
					}
				case CommandSignRequest:
					resp.Command = CommandSignResponse
					var req SignRequest
					var sig *crypto.Signature
					err := json.Unmarshal(out.Data, &req)
					var e string
					if err == nil {
						sig, err = w.WalletSign(cctx.Context, req.Signer, req.ToSign, req.Meta)
					}
					if err != nil {
						e = err.Error()
					}
					resp.Data, err = json.Marshal(
						&SignResponse{
							sig,
							e,
						})
					if err != nil {
						panic(err)
					}
				}
				err = c.WriteJSON(&resp)
				if err != nil {
					log.Fatalf("write: %+v", err)
					return
				}
			}
		}()

		for {
			select {
			case <-done:
				return nil
			case <-interrupt:
				err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				if err != nil {
					return xerrors.Errorf("write close: %+v", err)
				}
			}
		}
	},
}

func newRemoteWallet(mctx context.Context, info string) (*remotewallet.RemoteWallet, func(), error) {
	ai := cliutil.ParseApiInfo(info)

	url, err := ai.DialArgs()
	if err != nil {
		return nil, nil, err
	}

	var res apistruct.WalletStruct
	closer, err := jsonrpc.NewMergeClient(mctx, url, "Filecoin",
		[]interface{}{
			&res.Internal,
		},
		nil,
	)

	if err != nil {
		return nil, nil, xerrors.Errorf("creating jsonrpc clientWallet: %w", err)
	}

	return &remotewallet.RemoteWallet{WalletAPI: &res}, closer, nil
}
