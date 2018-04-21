package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/skycoin/skycoin/src/wallet"
	gcli "github.com/urfave/cli"
)

func encryptWalletCmd(cfg Config) gcli.Command {
	name := "encryptWallet"
	return gcli.Command{
		Name:      name,
		Usage:     "Encrypt wallet",
		ArgsUsage: "[wallet id]",
		Description: fmt.Sprintf(`
		The default wallet (%s) will be
		used if no wallet was specified.
		
		Use caution when using the "-p" command. If you have command history enabled 
		your wallet encryption password can be recovered from the history log. If you
		do not include the "-p" option you will be prompted to enter your password
		after you enter your command.`, cfg.FullWalletPath()),
		Flags: []gcli.Flag{
			gcli.StringFlag{
				Name:  "p",
				Usage: "[password] Wallet password",
			},
			gcli.StringFlag{
				Name:  "x,crypto-type",
				Value: string(wallet.CryptoTypeScryptChacha20poly1305),
				Usage: "[crypto type] The crypto type for wallet encryption, can be scrypt-chacha20poly1305 or sha256-xor",
			},
		},
		OnUsageError: onCommandUsageError(name),
		Action: func(c *gcli.Context) error {
			cfg := ConfigFromContext(c)

			w, err := resolveWalletPath(cfg, "")
			if err != nil {
				return err
			}

			cryptoType, err := wallet.CryptoTypeFromString(c.String("x"))
			if err != nil {
				errorWithHelp(c, err)
				return nil
			}

			wlt, err := encryptWallet(w, []byte(c.String("p")), cryptoType)
			switch err.(type) {
			case nil:
			case WalletLoadError:
				errorWithHelp(c, err)
				return nil
			case WalletSaveError:
				return errors.New("save wallet failed")
			default:
				return err
			}

			printJSON(wallet.NewReadableWallet(wlt))
			return nil
		},
	}
}

func encryptWallet(walletFile string, password []byte, cryptoType wallet.CryptoType) (*wallet.Wallet, error) {
	wlt, err := wallet.Load(walletFile)
	if err != nil {
		return nil, WalletLoadError{err}
	}

	if wlt.IsEncrypted() {
		return nil, wallet.ErrWalletEncrypted
	}

	if len(password) == 0 {
		var err error
		password, err = readPasswordFromTerminal()
		if err != nil {
			return nil, err
		}
	}

	if err := wlt.Lock(password, cryptoType); err != nil {
		return nil, err
	}

	dir, err := filepath.Abs(filepath.Dir(walletFile))
	if err != nil {
		return nil, err
	}

	// save the wallet
	if err := wlt.Save(dir); err != nil {
		return nil, WalletLoadError{err}
	}

	return wlt, nil
}
