// Copyright 2024 The go-ethereum Authors
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

package ethapi

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/rpc"
)

// Global footprint cache instance
var globalFootprintCache *FootprintCache

// InitFootprintCache initializes the global footprint cache and registers callbacks
func InitFootprintCache() {
	if globalFootprintCache == nil {
		globalFootprintCache = NewFootprintCache()

		// Register the global callbacks for state processor
		core.GlobalFootprintStore = func(txHash common.Hash, expectedFootprint, actualFootprint string, blockNumber uint64, mismatch bool) {
			globalFootprintCache.Store(txHash, expectedFootprint, actualFootprint, blockNumber, mismatch)
		}

		core.GlobalFootprintEvict = func(currentBlockNumber uint64) {
			globalFootprintCache.EvictOldEntries(currentBlockNumber)
		}
	}
}

// GetGlobalFootprintCache returns the global footprint cache instance
func GetGlobalFootprintCache() *FootprintCache {
	if globalFootprintCache == nil {
		InitFootprintCache()
	}
	return globalFootprintCache
}

// GetFootprintAPIs returns the collection of RPC services the footprint package offers.
// This should be called from the eth backend's APIs() method.
func GetFootprintAPIs() []rpc.API {
	cache := GetGlobalFootprintCache()

	return []rpc.API{
		{
			Namespace: "rome",
			Service:   NewFootprintAPI(cache),
			Public:    true,
		},
	}
}
