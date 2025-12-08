// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc/eip4844"
	"github.com/ethereum/go-ethereum/core/footprint"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

// ChainContext supports retrieving headers and consensus parameters from the
// current blockchain to be used during transaction processing.
type ChainContext interface {
	// Engine retrieves the chain's consensus engine.
	Engine() consensus.Engine

	// GetHeader returns the header corresponding to the hash/number argument pair.
	GetHeader(common.Hash, uint64) *types.Header

	// GetFootprintManager returns the footprint manager.
	GetFootprintManager() *footprint.Manager

	// GetSolanaMetadata retrieves the solana slot and hash recorded for a block hash.
	GetSolanaMetadata(common.Hash) (uint64, common.Hash, bool)
}

// NewEVMBlockContext creates a new context for use in the EVM.
// If solanaBlockNumber and solanaBlockHash are provided, they take precedence over hash lookup.
func NewEVMBlockContext(header *types.Header, chain ChainContext, author *common.Address, config *params.ChainConfig, statedb types.StateGetter, solanaBlockNumber *uint64, solanaBlockHash *common.Hash) vm.BlockContext {
	var (
		beneficiary common.Address
		baseFee     *big.Int
		blobBaseFee *big.Int
		random      *common.Hash
	)

	// If we don't have an explicit author (i.e. not mining), extract from the header
	if author == nil {
		beneficiary, _ = chain.Engine().Author(header) // Ignore error, we're past header validation
	} else {
		beneficiary = *author
	}
	if header.BaseFee != nil {
		baseFee = new(big.Int).Set(header.BaseFee)
	}
	if header.ExcessBlobGas != nil {
		blobBaseFee = eip4844.CalcBlobFee(*header.ExcessBlobGas)
	}
	if header.Difficulty.Cmp(common.Big0) == 0 {
		random = &header.MixDigest
	}
	var getSolanaHash func(uint64) (common.Hash, bool)
	var getSolanaHashByEthBlock func(uint64) (common.Hash, bool)
	
	if solanaBlockNumber == nil || solanaBlockHash == nil {
		if chain != nil {
			// Look up Solana metadata from database for current block
			if metaSlot, metaHash, ok := chain.GetSolanaMetadata(header.Hash()); ok {
				log.Info("NewEVMBlockContext: retrieved Solana metadata from chain", "blockHash", header.Hash().Hex(), "slot", metaSlot, "solanaHash", metaHash.Hex(), "blockNumber", header.Number.Uint64())
				solanaBlockNumber = &metaSlot
				solanaBlockHash = &metaHash
			} else {
				log.Warn("NewEVMBlockContext: Solana metadata not found in chain", "blockHash", header.Hash().Hex(), "blockNumber", header.Number.Uint64())
			}
		}
	} else {
		log.Info("NewEVMBlockContext: using provided Solana metadata", "blockHash", header.Hash().Hex(), "slot", *solanaBlockNumber, "solanaHash", solanaBlockHash.Hex(), "blockNumber", header.Number.Uint64())
	}
	
	if chain != nil {
		getSolanaHash = func(slot uint64) (common.Hash, bool) {
			log.Info("GetSolanaHash: searching for slot", "requestedSlot", slot, "currentSolanaSlot", solanaBlockNumber, "headerHash", header.Hash().Hex(), "headerNumber", header.Number.Uint64())
			if solanaBlockNumber != nil && *solanaBlockNumber == slot && solanaBlockHash != nil {
				log.Info("GetSolanaHash: found in current block being built", "slot", slot, "hash", solanaBlockHash.Hex())
				return *solanaBlockHash, true
			}
			if metaSlot, metaHash, ok := chain.GetSolanaMetadata(header.Hash()); ok {
				log.Info("GetSolanaHash: current header metadata", "headerSlot", metaSlot, "requestedSlot", slot, "match", metaSlot == slot)
				if metaSlot == slot {
					log.Info("GetSolanaHash: found in current header", "slot", slot, "hash", metaHash.Hex())
					return metaHash, true
				}
			} else {
				log.Info("GetSolanaHash: no metadata for current header", "headerHash", header.Hash().Hex())
			}
			// Start from parent since current block might not be inserted yet
			current := header
			for i := 0; i < 256; i++ {
				if current.ParentHash == (common.Hash{}) || current.Number == nil {
					log.Info("GetSolanaHash: reached genesis or invalid block", "i", i)
					break
				}
				if !current.Number.IsUint64() {
					log.Info("GetSolanaHash: block number overflow", "i", i)
					break
				}
				number := current.Number.Uint64()
				if number == 0 {
					log.Info("GetSolanaHash: reached genesis block", "i", i)
					break
				}
				parent := chain.GetHeader(current.ParentHash, number-1)
				if parent == nil {
					log.Info("GetSolanaHash: parent not found", "parentHash", current.ParentHash.Hex(), "parentNumber", number-1, "i", i)
					break
				}
				if metaSlot, metaHash, ok := chain.GetSolanaMetadata(parent.Hash()); ok {
					log.Info("GetSolanaHash: checking parent", "parentSlot", metaSlot, "requestedSlot", slot, "parentHash", parent.Hash().Hex(), "parentNumber", parent.Number.Uint64(), "i", i)
					if metaSlot == slot {
						log.Info("GetSolanaHash: found in parent block", "slot", slot, "hash", metaHash.Hex(), "parentNumber", parent.Number.Uint64())
						return metaHash, true
					}
				} else {
					log.Info("GetSolanaHash: no metadata for parent", "parentHash", parent.Hash().Hex(), "parentNumber", parent.Number.Uint64(), "i", i)
				}
				current = parent
			}
			log.Warn("GetSolanaHash: not found after searching", "requestedSlot", slot, "currentSolanaSlot", solanaBlockNumber)
			return common.Hash{}, false
		}
		getSolanaHashByEthBlock = func(ethBlockNum uint64) (common.Hash, bool) {
			offset := header.Number.Uint64() - ethBlockNum
			if offset > header.Number.Uint64() || ethBlockNum > header.Number.Uint64() {
				return common.Hash{}, false
			}
			for current := header; current != nil; {
				if !current.Number.IsUint64() {
					break
				}
				number := current.Number.Uint64()
				if number == ethBlockNum {
					if _, metaHash, ok := chain.GetSolanaMetadata(current.Hash()); ok {
						return metaHash, true
					}
					return common.Hash{}, false
				}
				if number < ethBlockNum || number == 0 {
					break
				}
				if current.ParentHash == (common.Hash{}) {
					break
				}
				current = chain.GetHeader(current.ParentHash, number-1)
			}
			return common.Hash{}, false
		}
	}

	blockCtx := vm.BlockContext{
		CanTransfer:          CanTransfer,
		Transfer:             Transfer,
		GetHash:              GetHashFn(header, chain),
		GetSolanaHash:        getSolanaHash,
		GetSolanaHashByEthBlock: getSolanaHashByEthBlock,
		Coinbase:             beneficiary,
		BlockNumber:          new(big.Int).Set(header.Number),
		Time:                 header.Time,
		Difficulty:           new(big.Int).Set(header.Difficulty),
		BaseFee:              baseFee,
		BlobBaseFee:          blobBaseFee,
		GasLimit:             header.GasLimit,
		Random:               random,
		L1CostFunc:           types.NewL1CostFunc(config, statedb),
		SolanaBlockNumber:    solanaBlockNumber,
		SolanaBlockHash:      solanaBlockHash,
	}
	if solanaBlockNumber != nil {
		log.Debug("NewEVMBlockContext: final context", "blockHash", header.Hash().Hex(), "solanaBlockNumber", *solanaBlockNumber, "hasSolanaHash", solanaBlockHash != nil, "hasGetSolanaHash", getSolanaHash != nil)
	} else {
		log.Debug("NewEVMBlockContext: final context (no Solana metadata)", "blockHash", header.Hash().Hex(), "hasGetSolanaHash", getSolanaHash != nil)
	}
	return blockCtx
}

// NewEVMTxContext creates a new transaction context for a single transaction.
func NewEVMTxContext(msg *Message) vm.TxContext {
	ctx := vm.TxContext{
		Origin:     msg.From,
		GasPrice:   new(big.Int).Set(msg.GasPrice),
		BlobHashes: msg.BlobHashes,
		GasLimit:   msg.GasLimit,
	}
	if msg.BlobGasFeeCap != nil {
		ctx.BlobFeeCap = new(big.Int).Set(msg.BlobGasFeeCap)
	}
	return ctx
}

// GetHashFn returns a GetHashFunc which retrieves header hashes by number
func GetHashFn(ref *types.Header, chain ChainContext) func(n uint64) common.Hash {
	// Cache will initially contain [refHash.parent],
	// Then fill up with [refHash.p, refHash.pp, refHash.ppp, ...]
	var cache []common.Hash

	return func(n uint64) common.Hash {
		if ref.Number.Uint64() <= n {
			// This situation can happen if we're doing tracing and using
			// block overrides.
			return common.Hash{}
		}
		// If there's no hash cache yet, make one
		if len(cache) == 0 {
			cache = append(cache, ref.ParentHash)
		}
		if idx := ref.Number.Uint64() - n - 1; idx < uint64(len(cache)) {
			return cache[idx]
		}
		// No luck in the cache, but we can start iterating from the last element we already know
		lastKnownHash := cache[len(cache)-1]
		lastKnownNumber := ref.Number.Uint64() - uint64(len(cache))

		for {
			header := chain.GetHeader(lastKnownHash, lastKnownNumber)
			if header == nil {
				break
			}
			cache = append(cache, header.ParentHash)
			lastKnownHash = header.ParentHash
			lastKnownNumber = header.Number.Uint64() - 1
			if n == lastKnownNumber {
				return lastKnownHash
			}
		}
		return common.Hash{}
	}
}

// CanTransfer checks whether there are enough funds in the address' account to make a transfer.
// This does not take the necessary gas in to account to make the transfer valid.
func CanTransfer(db vm.StateDB, addr common.Address, amount *big.Int) bool {
	return db.GetBalance(addr).Cmp(amount) >= 0
}

// Transfer subtracts amount from sender and adds amount to recipient using the given Db
func Transfer(db vm.StateDB, sender, recipient common.Address, amount *big.Int) {
	db.SubBalance(sender, amount)
	db.AddBalance(recipient, amount)
}
