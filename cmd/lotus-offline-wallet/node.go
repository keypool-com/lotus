package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/filecoin-project/lotus/lib/tablewriter"
	"github.com/ipfs/go-cid"
	"github.com/urfave/cli/v2"
	"os"
)

var nodeCmd = &cli.Command{
	Name:  "node",
	Usage: "Manage remote node",
	Flags: []cli.Flag {
			&cli.StringFlag{
			Name:  "server",
			Usage: "host address and port the remote api will listen on",
			Value: "127.0.0.1:1777",
		},
	},
	Subcommands: []*cli.Command{
		nodeList,
		nodeImport,
		nodeDelete,
		nodePendingCmd,
		nodeSubmitCmd,
	},
}

var nodeList = &cli.Command{
	Name:  "list",
	Usage: "List wallet address",
	Flags: []cli.Flag{
	},
	Action: func(cctx *cli.Context) error {
		client, err := apiClient(cctx)
		if err != nil {
			return err
		}

		ctx := lcli.ReqContext(cctx)

		addrs, err := client.WalletList(ctx)
		if err != nil {
			return err
		}

		for _, addr := range addrs {
			fmt.Println(addr.String())
		}

		return nil
	},
}

var nodeImport = &cli.Command{
	Name:      "import",
	Usage:     "import address",
	Action: func(cctx *cli.Context) error {
		ctx := lcli.ReqContext(cctx)

		if cctx.Args().Len() == 0 {
			return fmt.Errorf("require an valid address")
		}

		addr, err := address.NewFromString(cctx.Args().First())
		if err != nil {
			return err
		}

		var ki types.KeyInfo
		switch addr.Protocol() {
		case address.SECP256K1:
			ki.Type = types.KTSecp256k1
		case address.BLS:
			ki.Type = types.KTBLS
		default:
			return fmt.Errorf("unrecognized key type: %d", addr.Protocol())
		}
		ki.PrivateKey = []byte(addr.String())

		api, err := apiClient(cctx)
		if err != nil {
			return nil
		}

		addr, err = api.WalletImport(ctx, &ki)
		if err != nil {
			return err
		}

		fmt.Printf("imported key %s successfully!\n", addr)
		return nil
	},
}

var nodeDelete = &cli.Command{
	Name:      "delete",
	Usage:     "Delete an account from the wallet",
	ArgsUsage: "<address>",
	Action: func(cctx *cli.Context) error {
		ctx := lcli.ReqContext(cctx)

		if !cctx.Args().Present() || cctx.NArg() != 1 {
			return fmt.Errorf("must specify address to delete")
		}

		addr, err := address.NewFromString(cctx.Args().First())
		if err != nil {
			return err
		}

		api, err := apiClient(cctx)
		if err != nil {
			return err
		}

		return api.WalletDelete(ctx, addr)
	},
}

var nodePendingCmd = &cli.Command{
	Name:  "pending",
	Usage: "Query all pending messages to sign",
	Action: func(cctx *cli.Context) error {
		client, err := apiClient(cctx)
		if err != nil {
			return err
		}

		msgs, err := client.GetPendingMessages(lcli.ReqContext(cctx))
		if err != nil {
			return err
		}

		tab := tablewriter.New(tablewriter.Col("Address"), tablewriter.Col("Cid"), tablewriter.NewLineCol("Message"))

		for _, m := range msgs {
			_, cid, err := cid.CidFromBytes(m.ToSign)
			if err != nil {
				return nil
			}

			var buf bytes.Buffer

			err = m.MarshalCBOR(&buf)
			if err != nil {
				return err
			}

			tab.Write(map[string]interface{}{
				"Address": m.Address.String(),
				"Cid": cid.String(),
				"Message": hex.EncodeToString(buf.Bytes()),
			})
		}

		return tab.Flush(os.Stdout)
	},
}

var nodeSubmitCmd = &cli.Command{
	Name:  "submit",
	Usage: "submit <signed message hex>",
	Flags: []cli.Flag{
	},
	Action: func(cctx *cli.Context) error {

		if cctx.NArg() != 1 {
			return fmt.Errorf("require signed message")
		}

		data, err := hex.DecodeString(cctx.Args().First())
		if err != nil {
			return err
		}

		var signed SignedMessage
		err = signed.UnmarshalCBOR(bytes.NewBuffer(data))
		if err != nil {
			return err
		}

		client, err := apiClient(cctx)
		if err != nil {
			return err
		}

		err = client.SubmitSignature(lcli.ReqContext(cctx), &signed)
		if err != nil {
			return err
		}

		_, cid, err := cid.CidFromBytes(signed.ToSign)
		if err != nil {
			return err
		}

		fmt.Printf("%v\n", cid)

		return nil
	},
}