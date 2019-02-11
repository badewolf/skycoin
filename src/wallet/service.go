package wallet

import (
	"fmt"
	"os"
	"sync"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/coin"
)

// BalanceGetter interface for getting the balance of given addresses
type BalanceGetter interface {
	GetBalanceOfAddrs(addrs []cipher.Address) ([]BalancePair, error)
}

// Service wallet service struct
type Service struct {
	sync.RWMutex
	wallets         Wallets
	firstAddrIDMap  map[string]string // Key: first address in wallet; Value: wallet id
	walletDirectory string
	cryptoType      CryptoType
	enableWalletAPI bool
	enableSeedAPI   bool
}

// Config wallet service config
type Config struct {
	WalletDir       string
	CryptoType      CryptoType
	EnableWalletAPI bool
	EnableSeedAPI   bool
}

// NewService new wallet service
func NewService(c Config) (*Service, error) {
	serv := &Service{
		firstAddrIDMap:  make(map[string]string),
		cryptoType:      c.CryptoType,
		enableWalletAPI: c.EnableWalletAPI,
		enableSeedAPI:   c.EnableSeedAPI,
	}

	if !serv.enableWalletAPI {
		return serv, nil
	}

	if err := os.MkdirAll(c.WalletDir, os.FileMode(0700)); err != nil {
		return nil, fmt.Errorf("failed to create wallet directory %s: %v", c.WalletDir, err)
	}

	serv.walletDirectory = c.WalletDir

	// Removes .wlt.bak files before loading wallets
	if err := removeBackupFiles(serv.walletDirectory); err != nil {
		return nil, fmt.Errorf("remove .wlt.bak files in %v failed: %v", serv.walletDirectory, err)
	}

	// Load wallets from disk
	w, err := LoadWallets(serv.walletDirectory)
	if err != nil {
		return nil, fmt.Errorf("failed to load all wallets: %v", err)
	}

	// Abort if there are duplicate wallets on disk
	if wltID, addr, hasDup := w.containsDuplicate(); hasDup {
		return nil, fmt.Errorf("duplicate wallet found with initial address %s in file %q", addr, wltID)
	}

	// Abort if there are empty wallets on disk
	if wltID, hasEmpty := w.containsEmpty(); hasEmpty {
		return nil, fmt.Errorf("empty wallet file found: %q", wltID)
	}

	serv.setWallets(w)

	return serv, nil
}

// CreateWallet creates a wallet with the given wallet file name and options.
// A address will be automatically generated by default.
func (serv *Service) CreateWallet(wltName string, options Options, bg BalanceGetter) (*Wallet, error) {
	serv.Lock()
	defer serv.Unlock()
	if !serv.enableWalletAPI {
		return nil, ErrWalletAPIDisabled
	}
	if wltName == "" {
		wltName = serv.generateUniqueWalletFilename()
	}

	return serv.loadWallet(wltName, options, bg)
}

// loadWallet loads wallet from seed and scan the first N addresses
func (serv *Service) loadWallet(wltName string, options Options, bg BalanceGetter) (*Wallet, error) {
	// service decides what crypto type the wallet should use.
	if options.Encrypt {
		options.CryptoType = serv.cryptoType
	}

	w, err := NewWalletScanAhead(wltName, options, bg)
	if err != nil {
		return nil, err
	}

	// Check for duplicate wallets by initial seed
	if _, ok := serv.firstAddrIDMap[w.Entries[0].Address.String()]; ok {
		return nil, ErrSeedUsed
	}

	if err := serv.wallets.add(w); err != nil {
		return nil, err
	}

	if err := w.Save(serv.walletDirectory); err != nil {
		// If save fails, remove the added wallet
		serv.wallets.remove(w.Filename())
		return nil, err
	}

	serv.firstAddrIDMap[w.Entries[0].Address.String()] = w.Filename()

	return w.clone(), nil
}

func (serv *Service) generateUniqueWalletFilename() string {
	wltName := NewWalletFilename()
	for {
		if w := serv.wallets.get(wltName); w == nil {
			break
		}
		wltName = NewWalletFilename()
	}

	return wltName
}

// EncryptWallet encrypts wallet with password
func (serv *Service) EncryptWallet(wltID string, password []byte) (*Wallet, error) {
	serv.Lock()
	defer serv.Unlock()
	if !serv.enableWalletAPI {
		return nil, ErrWalletAPIDisabled
	}

	w, err := serv.getWallet(wltID)
	if err != nil {
		return nil, err
	}

	if w.IsEncrypted() {
		return nil, ErrWalletEncrypted
	}

	if err := w.Lock(password, serv.cryptoType); err != nil {
		return nil, err
	}

	// Save to disk first
	if err := w.Save(serv.walletDirectory); err != nil {
		return nil, err
	}

	// Sets the encrypted wallet
	serv.wallets.set(w)
	return w, nil
}

// DecryptWallet decrypts wallet with password
func (serv *Service) DecryptWallet(wltID string, password []byte) (*Wallet, error) {
	serv.Lock()
	defer serv.Unlock()
	if !serv.enableWalletAPI {
		return nil, ErrWalletAPIDisabled
	}

	w, err := serv.getWallet(wltID)
	if err != nil {
		return nil, err
	}

	// Returns error if wallet is not encrypted
	if !w.IsEncrypted() {
		return nil, ErrWalletNotEncrypted
	}

	// Unlocks the wallet
	unlockWlt, err := w.Unlock(password)
	if err != nil {
		return nil, err
	}

	// Updates the wallet file
	if err := unlockWlt.Save(serv.walletDirectory); err != nil {
		return nil, err
	}

	// Sets the decrypted wallet in memory
	serv.wallets.set(unlockWlt)
	return unlockWlt, nil
}

// NewAddresses generate address entries in given wallet,
// return nil if wallet does not exist.
// Set password as nil if the wallet is not encrypted, otherwise the password must be provided.
func (serv *Service) NewAddresses(wltID string, password []byte, num uint64) ([]cipher.Address, error) {
	serv.Lock()
	defer serv.Unlock()

	if !serv.enableWalletAPI {
		return nil, ErrWalletAPIDisabled
	}

	w, err := serv.getWallet(wltID)
	if err != nil {
		return nil, err
	}

	var addrs []cipher.Address
	f := func(wlt *Wallet) error {
		var err error
		addrs, err = wlt.GenerateSkycoinAddresses(num)
		return err
	}

	if w.IsEncrypted() {
		if err := w.GuardUpdate(password, f); err != nil {
			return nil, err
		}
	} else {
		if len(password) != 0 {
			return nil, ErrWalletNotEncrypted
		}

		if err := f(w); err != nil {
			return nil, err
		}
	}

	// Save the wallet first
	if err := w.Save(serv.walletDirectory); err != nil {
		return nil, err
	}

	serv.wallets.set(w)

	return addrs, nil
}

// GetSkycoinAddresses returns all addresses in given wallet
func (serv *Service) GetSkycoinAddresses(wltID string) ([]cipher.Address, error) {
	serv.RLock()
	defer serv.RUnlock()
	if !serv.enableWalletAPI {
		return nil, ErrWalletAPIDisabled
	}

	w, err := serv.getWallet(wltID)
	if err != nil {
		return nil, err
	}

	return w.GetSkycoinAddresses()
}

// GetWallet returns wallet by id
func (serv *Service) GetWallet(wltID string) (*Wallet, error) {
	serv.RLock()
	defer serv.RUnlock()
	if !serv.enableWalletAPI {
		return nil, ErrWalletAPIDisabled
	}

	return serv.getWallet(wltID)
}

// returns the clone of the wallet of given id
func (serv *Service) getWallet(wltID string) (*Wallet, error) {
	w := serv.wallets.get(wltID)
	if w == nil {
		return nil, ErrWalletNotExist
	}
	return w.clone(), nil
}

// GetWallets returns all wallet clones
func (serv *Service) GetWallets() (Wallets, error) {
	serv.RLock()
	defer serv.RUnlock()
	if !serv.enableWalletAPI {
		return nil, ErrWalletAPIDisabled
	}

	wlts := make(Wallets, len(serv.wallets))
	for k, w := range serv.wallets {
		wlts[k] = w.clone()
	}
	return wlts, nil
}

// CreateTransaction creates and signs a transaction based upon CreateTransactionParams.
// Set the password as nil if the wallet is not encrypted, otherwise the password must be provided
func (serv *Service) CreateTransaction(params CreateTransactionParams, auxs coin.AddressUxOuts, headTime uint64) (*coin.Transaction, []UxBalance, error) {
	serv.RLock()
	defer serv.RUnlock()

	if !serv.enableWalletAPI {
		return nil, nil, ErrWalletAPIDisabled
	}

	if err := params.Validate(); err != nil {
		return nil, nil, err
	}

	w, err := serv.getWallet(params.Wallet.ID)
	if err != nil {
		return nil, nil, err
	}

	// Check if the wallet needs a password
	if w.IsEncrypted() {
		if len(params.Wallet.Password) == 0 {
			return nil, nil, ErrMissingPassword
		}
	} else {
		if len(params.Wallet.Password) != 0 {
			return nil, nil, ErrWalletNotEncrypted
		}
	}

	var tx *coin.Transaction
	var inputs []UxBalance
	if w.IsEncrypted() {
		err = w.GuardView(params.Wallet.Password, func(wlt *Wallet) error {
			var err error
			tx, inputs, err = wlt.CreateTransaction(params, auxs, headTime)
			return err
		})
	} else {
		tx, inputs, err = w.CreateTransaction(params, auxs, headTime)
	}
	if err != nil {
		return nil, nil, err
	}

	return tx, inputs, nil
}

// UpdateWalletLabel updates the wallet label
func (serv *Service) UpdateWalletLabel(wltID, label string) error {
	serv.Lock()
	defer serv.Unlock()
	if !serv.enableWalletAPI {
		return ErrWalletAPIDisabled
	}

	w, err := serv.getWallet(wltID)
	if err != nil {
		return err
	}

	w.setLabel(label)

	if err := w.Save(serv.walletDirectory); err != nil {
		return err
	}

	serv.wallets.set(w)
	return nil
}

// Remove removes wallet of given wallet id from the service
func (serv *Service) Remove(wltID string) error {
	serv.Lock()
	defer serv.Unlock()
	if !serv.enableWalletAPI {
		return ErrWalletAPIDisabled
	}

	wlt := serv.wallets.get(wltID)
	if wlt != nil && len(wlt.Entries) > 0 {
		addr := wlt.Entries[0].Address.String()
		delete(serv.firstAddrIDMap, addr)
	}

	serv.wallets.remove(wltID)
	return nil
}

func (serv *Service) setWallets(wlts Wallets) {
	serv.wallets = wlts

	for wltID, wlt := range wlts {
		addr := wlt.Entries[0].Address.String()
		serv.firstAddrIDMap[addr] = wltID
	}
}

// GetWalletSeed returns seed of encrypted wallet of given wallet id
// Returns ErrWalletNotEncrypted if it's not encrypted
func (serv *Service) GetWalletSeed(wltID string, password []byte) (string, error) {
	serv.RLock()
	defer serv.RUnlock()
	if !serv.enableWalletAPI {
		return "", ErrWalletAPIDisabled
	}

	if !serv.enableSeedAPI {
		return "", ErrSeedAPIDisabled
	}

	w, err := serv.getWallet(wltID)
	if err != nil {
		return "", err
	}

	if !w.IsEncrypted() {
		return "", ErrWalletNotEncrypted
	}

	var seed string
	if err := w.GuardView(password, func(wlt *Wallet) error {
		seed = wlt.seed()
		return nil
	}); err != nil {
		return "", err
	}

	return seed, nil
}

// UpdateSecrets opens a wallet for modification of secret data and saves it safely
func (serv *Service) UpdateSecrets(wltID string, password []byte, f func(*Wallet) error) error {
	serv.Lock()
	defer serv.Unlock()
	if !serv.enableWalletAPI {
		return ErrWalletAPIDisabled
	}

	w, err := serv.getWallet(wltID)
	if err != nil {
		return err
	}

	if w.IsEncrypted() {
		if err := w.GuardUpdate(password, f); err != nil {
			return err
		}
	} else if len(password) != 0 {
		return ErrWalletNotEncrypted
	} else {
		if err := f(w); err != nil {
			return err
		}
	}

	// Save the wallet first
	if err := w.Save(serv.walletDirectory); err != nil {
		return err
	}

	serv.wallets.set(w)

	return nil
}

// Update opens a wallet for modification of non-secret data and saves it safely
func (serv *Service) Update(wltID string, f func(*Wallet) error) error {
	serv.Lock()
	defer serv.Unlock()
	if !serv.enableWalletAPI {
		return ErrWalletAPIDisabled
	}

	w, err := serv.getWallet(wltID)
	if err != nil {
		return err
	}

	if err := f(w); err != nil {
		return err
	}

	// Save the wallet first
	if err := w.Save(serv.walletDirectory); err != nil {
		return err
	}

	serv.wallets.set(w)

	return nil
}

// ViewSecrets opens a wallet for reading secret data
func (serv *Service) ViewSecrets(wltID string, password []byte, f func(*Wallet) error) error {
	serv.RLock()
	defer serv.RUnlock()
	if !serv.enableWalletAPI {
		return ErrWalletAPIDisabled
	}

	w, err := serv.getWallet(wltID)
	if err != nil {
		return err
	}

	if w.IsEncrypted() {
		return w.GuardView(password, f)
	} else if len(password) != 0 {
		return ErrWalletNotEncrypted
	} else {
		return f(w)
	}
}

// View opens a wallet for reading non-secret data
func (serv *Service) View(wltID string, f func(*Wallet) error) error {
	serv.RLock()
	defer serv.RUnlock()
	if !serv.enableWalletAPI {
		return ErrWalletAPIDisabled
	}

	w, err := serv.getWallet(wltID)
	if err != nil {
		return err
	}

	return f(w)
}

// RecoverWallet recovers an encrypted wallet from seed.
// The recovered wallet will be encrypted with the new password, if provided.
func (serv *Service) RecoverWallet(wltName, seed string, password []byte) (*Wallet, error) {
	serv.Lock()
	defer serv.Unlock()
	if !serv.enableWalletAPI {
		return nil, ErrWalletAPIDisabled
	}

	w, err := serv.getWallet(wltName)
	if err != nil {
		return nil, err
	}

	if !w.IsEncrypted() {
		return nil, ErrWalletNotEncrypted
	}

	if w.Type() != WalletTypeDeterministic {
		return nil, ErrWalletNotDeterministic
	}

	// Generate the first address from the seed
	var pk cipher.PubKey
	pk, _, err = cipher.GenerateDeterministicKeyPair([]byte(seed))
	if err != nil {
		return nil, err
	}
	addr := w.addressConstructor()(pk)

	// Compare to the wallet's first address
	if addr != w.Entries[0].Address {
		return nil, ErrWalletRecoverSeedWrong
	}

	// Create a new wallet with the same number of addresses, encrypting if needed
	w2, err := NewWallet(wltName, Options{
		Coin:       w.coin(),
		Label:      w.Label(),
		Seed:       seed,
		Encrypt:    len(password) != 0,
		Password:   password,
		CryptoType: w.cryptoType(),
		GenerateN:  uint64(len(w.Entries)),
	})
	if err != nil {
		return nil, err
	}

	// Preserve the timestamp of the old wallet
	w2.setTimestamp(w.timestamp())

	// Save to disk
	if err := w2.Save(serv.walletDirectory); err != nil {
		return nil, err
	}

	serv.wallets.set(w2)

	return w2.clone(), nil
}
