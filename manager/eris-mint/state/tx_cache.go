// Copyright 2017 Monax Industries Limited
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package state

import (
	"fmt"

	acm "github.com/monax/eris-db/account"
	"github.com/monax/eris-db/common/sanity"
	"github.com/monax/eris-db/manager/eris-mint/evm"
	ptypes "github.com/monax/eris-db/permission/types" // for GlobalPermissionAddress ...
	"github.com/monax/eris-db/txs"
	. "github.com/monax/eris-db/word256"

	"github.com/tendermint/go-crypto"
)

type TxCache struct {
	backend  *BlockCache
	accounts map[Word256]vmAccountInfo
	storages map[Tuple256]Word256
}

var _ vm.AppState = &TxCache{}

func NewTxCache(backend *BlockCache) *TxCache {
	return &TxCache{
		backend:  backend,
		accounts: make(map[Word256]vmAccountInfo),
		storages: make(map[Tuple256]Word256),
	}
}

//-------------------------------------
// TxCache.account

func (cache *TxCache) GetAccount(addr Word256) *vm.Account {
	acc, removed := cache.accounts[addr].unpack()
	if removed {
		return nil
	} else if acc == nil {
		acc2 := cache.backend.GetAccount(addr.Postfix(20))
		if acc2 != nil {
			return toVMAccount(acc2)
		}
	}
	return acc
}

func (cache *TxCache) UpdateAccount(acc *vm.Account) {
	addr := acc.Address
	_, removed := cache.accounts[addr].unpack()
	if removed {
		sanity.PanicSanity("UpdateAccount on a removed account")
	}
	cache.accounts[addr] = vmAccountInfo{acc, false}
}

func (cache *TxCache) RemoveAccount(acc *vm.Account) {
	addr := acc.Address
	_, removed := cache.accounts[addr].unpack()
	if removed {
		sanity.PanicSanity("RemoveAccount on a removed account")
	}
	cache.accounts[addr] = vmAccountInfo{acc, true}
}

// Creates a 20 byte address and bumps the creator's nonce.
func (cache *TxCache) CreateAccount(creator *vm.Account) *vm.Account {

	// Generate an address
	nonce := creator.Nonce
	creator.Nonce += 1

	addr := LeftPadWord256(NewContractAddress(creator.Address.Postfix(20), int(nonce)))

	// Create account from address.
	account, removed := cache.accounts[addr].unpack()
	if removed || account == nil {
		account = &vm.Account{
			Address:     addr,
			Balance:     0,
			Code:        nil,
			Nonce:       0,
			Permissions: cache.GetAccount(ptypes.GlobalPermissionsAddress256).Permissions,
			Other: vmAccountOther{
				PubKey:      nil,
				StorageRoot: nil,
			},
		}
		cache.accounts[addr] = vmAccountInfo{account, false}
		return account
	} else {
		// either we've messed up nonce handling, or sha3 is broken
		sanity.PanicSanity(fmt.Sprintf("Could not create account, address already exists: %X", addr))
		return nil
	}
}

// TxCache.account
//-------------------------------------
// TxCache.storage

func (cache *TxCache) GetStorage(addr Word256, key Word256) Word256 {
	// Check cache
	value, ok := cache.storages[Tuple256{addr, key}]
	if ok {
		return value
	}

	// Load from backend
	return cache.backend.GetStorage(addr, key)
}

// NOTE: Set value to zero to removed from the trie.
func (cache *TxCache) SetStorage(addr Word256, key Word256, value Word256) {
	_, removed := cache.accounts[addr].unpack()
	if removed {
		sanity.PanicSanity("SetStorage() on a removed account")
	}
	cache.storages[Tuple256{addr, key}] = value
}

// TxCache.storage
//-------------------------------------

// These updates do not have to be in deterministic order,
// the backend is responsible for ordering updates.
func (cache *TxCache) Sync() {
	// Remove or update storage
	for addrKey, value := range cache.storages {
		addr, key := Tuple256Split(addrKey)
		cache.backend.SetStorage(addr, key, value)
	}

	// Remove or update accounts
	for addr, accInfo := range cache.accounts {
		acc, removed := accInfo.unpack()
		if removed {
			cache.backend.RemoveAccount(addr.Postfix(20))
		} else {
			cache.backend.UpdateAccount(toStateAccount(acc))
		}
	}
}

//-----------------------------------------------------------------------------

// Convenience function to return address of new contract
func NewContractAddress(caller []byte, nonce int) []byte {
	return txs.NewContractAddress(caller, nonce)
}

// Converts backend.Account to vm.Account struct.
func toVMAccount(acc *acm.Account) *vm.Account {
	return &vm.Account{
		Address:     LeftPadWord256(acc.Address),
		Balance:     acc.Balance,
		Code:        acc.Code, // This is crazy.
		Nonce:       int64(acc.Sequence),
		Permissions: acc.Permissions, // Copy
		Other: vmAccountOther{
			PubKey:      acc.PubKey,
			StorageRoot: acc.StorageRoot,
		},
	}
}

// Converts vm.Account to backend.Account struct.
func toStateAccount(acc *vm.Account) *acm.Account {
	var pubKey crypto.PubKey
	var storageRoot []byte
	if acc.Other != nil {
		pubKey, storageRoot = acc.Other.(vmAccountOther).unpack()
	}

	return &acm.Account{
		Address:     acc.Address.Postfix(20),
		PubKey:      pubKey,
		Balance:     acc.Balance,
		Code:        acc.Code,
		Sequence:    int(acc.Nonce),
		StorageRoot: storageRoot,
		Permissions: acc.Permissions, // Copy
	}
}

// Everything in acmAccount that doesn't belong in
// exported vmAccount fields.
type vmAccountOther struct {
	PubKey      crypto.PubKey
	StorageRoot []byte
}

func (accOther vmAccountOther) unpack() (crypto.PubKey, []byte) {
	return accOther.PubKey, accOther.StorageRoot
}

type vmAccountInfo struct {
	account *vm.Account
	removed bool
}

func (accInfo vmAccountInfo) unpack() (*vm.Account, bool) {
	return accInfo.account, accInfo.removed
}
