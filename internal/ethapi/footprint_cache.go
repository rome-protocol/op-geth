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
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

// FootprintEntry represents a cached state footprint entry
type FootprintEntry struct {
	ExpectedFootprint string    // The expected footprint from Rome-EVM
	ActualFootprint   string    // The actual footprint from op-geth
	BlockNumber       uint64    // Block number when the transaction was processed
	Timestamp         time.Time // When the entry was created
	Mismatch          bool      // Whether there was a footprint mismatch
}

// FootprintCache manages state footprint entries with automatic eviction
type FootprintCache struct {
	mu      sync.RWMutex
	entries map[common.Hash]*FootprintEntry
	maxAge  uint64 // Maximum age in blocks (12 blocks)
}

// NewFootprintCache creates a new footprint cache
func NewFootprintCache() *FootprintCache {
	return &FootprintCache{
		entries: make(map[common.Hash]*FootprintEntry),
		maxAge:  12,
	}
}

// Store stores a footprint entry for a transaction
func (fc *FootprintCache) Store(txHash common.Hash, expectedFootprint, actualFootprint string, blockNumber uint64, mismatch bool) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	fc.entries[txHash] = &FootprintEntry{
		ExpectedFootprint: expectedFootprint,
		ActualFootprint:   actualFootprint,
		BlockNumber:       blockNumber,
		Timestamp:         time.Now(),
		Mismatch:          mismatch,
	}

	log.Debug("Stored footprint entry", "txHash", txHash.Hex(), "blockNumber", blockNumber, "mismatch", mismatch)
}

// Get retrieves a footprint entry for a transaction
func (fc *FootprintCache) Get(txHash common.Hash) (*FootprintEntry, bool) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	entry, exists := fc.entries[txHash]
	return entry, exists
}

// EvictOldEntries removes entries older than maxAge blocks from the current block
func (fc *FootprintCache) EvictOldEntries(currentBlockNumber uint64) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	evictedCount := 0
	for txHash, entry := range fc.entries {
		if currentBlockNumber-entry.BlockNumber > fc.maxAge {
			delete(fc.entries, txHash)
			evictedCount++
		}
	}

	if evictedCount > 0 {
		log.Debug("Evicted old footprint entries", "count", evictedCount, "currentBlock", currentBlockNumber)
	}
}

// GetStats returns cache statistics
func (fc *FootprintCache) GetStats() map[string]interface{} {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	mismatchCount := 0
	for _, entry := range fc.entries {
		if entry.Mismatch {
			mismatchCount++
		}
	}

	return map[string]interface{}{
		"totalEntries":  len(fc.entries),
		"mismatchCount": mismatchCount,
		"maxAge":        fc.maxAge,
	}
}

// Clear removes all entries from the cache
func (fc *FootprintCache) Clear() {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	fc.entries = make(map[common.Hash]*FootprintEntry)
	log.Debug("Cleared all footprint entries")
}

// FootprintAPI provides an API to query state footprints
type FootprintAPI struct {
	cache *FootprintCache
}

// NewFootprintAPI creates a new footprint API
func NewFootprintAPI(cache *FootprintCache) *FootprintAPI {
	return &FootprintAPI{
		cache: cache,
	}
}

// GetFootprint returns the footprint information for a given transaction hash
func (api *FootprintAPI) GetFootprint(txHash common.Hash) (*FootprintEntry, error) {
	entry, exists := api.cache.Get(txHash)
	if !exists {
		return nil, nil // Return nil, nil to indicate not found (following Go conventions)
	}
	return entry, nil
}

// GetFootprintStats returns statistics about the footprint cache
func (api *FootprintAPI) GetFootprintStats() map[string]interface{} {
	return api.cache.GetStats()
}
