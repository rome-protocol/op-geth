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

package footprint

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

// DefaultMaxMismatchEntries is the default maximum number of known mismatch entries
// to keep in the file and memory. This prevents unbounded disk and memory growth.
const DefaultMaxMismatchEntries uint64 = 10000

// Entry represents a cached state footprint entry
type Entry struct {
	ExpectedFootprint string 
	ActualFootprint   string 
	BlockNumber       uint64
	Mismatch          bool  
}

// Manager handles both footprint caching and mismatch tracking
type Manager struct {
	mu                 sync.RWMutex
	cache              map[common.Hash]*Entry       
	knownMismatches    map[common.Hash]bool        
	mismatchFile       string                       
	maxCacheAge        uint64                       
	maxMismatchEntries uint64                       
}

var (
	globalManager     *Manager
	globalManagerOnce sync.Once
)

// GetManager returns the global footprint Manager.
func GetManager(dataDir string) *Manager {
	globalManagerOnce.Do(func() {
		mismatchFile := filepath.Join(dataDir, "known_footprint_mismatches.txt")		
		maxMismatchEntries := DefaultMaxMismatchEntries
		if envMax := os.Getenv("GETH_FOOTPRINT_MAX_MISMATCHES"); envMax != "" {
			if parsed, err := strconv.ParseUint(envMax, 10, 64); err == nil && parsed > 0 {
				maxMismatchEntries = parsed
			} else {
				log.Warn("Invalid GETH_FOOTPRINT_MAX_MISMATCHES value, using default", "value", envMax, "default", maxMismatchEntries)
			}
		}
		
		globalManager = &Manager{
			cache:              make(map[common.Hash]*Entry),
			knownMismatches:    make(map[common.Hash]bool),
			mismatchFile:       mismatchFile,
			maxCacheAge:        12,
			maxMismatchEntries: maxMismatchEntries,
		}
		globalManager.loadKnownMismatches()
	})
	return globalManager
}

// loadKnownMismatches reads known mismatch tx hashes from disk
func (m *Manager) loadKnownMismatches() {
	file, err := os.Open(m.mismatchFile)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		return
	}
	defer file.Close()

	// Read all valid entries first
	var entries []common.Hash
	scanner := bufio.NewScanner(file)
	totalCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		txHash := common.HexToHash(line)
		entries = append(entries, txHash)
		totalCount++
	}

	if err := scanner.Err(); err != nil {
		return
	}

	// Only keep the most recent maxMismatchEntries entries
	loadedCount := len(entries)
	if uint64(len(entries)) > m.maxMismatchEntries {
		entries = entries[len(entries)-int(m.maxMismatchEntries):]
		// Rewrite file with truncated entries
		m.writeMismatchFile(entries)
	}

	// Load entries into memory map
	for _, txHash := range entries {
		m.knownMismatches[txHash] = true
	}

	if loadedCount > 0 {
		log.Info("Loaded known footprint mismatches", "count", len(entries), "path", m.mismatchFile)
	}
}

// IsKnownMismatch checks if a transaction hash is in the known mismatches list
func (m *Manager) IsKnownMismatch(txHash common.Hash) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.knownMismatches[txHash]
}

// writeMismatchFile writes the given entries to the mismatch file, truncating it first.
func (m *Manager) writeMismatchFile(entries []common.Hash) error {
	file, err := os.OpenFile(m.mismatchFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, txHash := range entries {
		if _, err := file.WriteString(txHash.Hex() + "\n"); err != nil {
			return err
		}
	}
	return nil
}

// RecordMismatch adds a new mismatch to the known list and persists to disk.
func (m *Manager) RecordMismatch(txHash common.Hash) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already known
	if m.knownMismatches[txHash] {
		return nil
	}

	// Add to in-memory map
	m.knownMismatches[txHash] = true

	// Read existing entries from file to maintain order
	var entries []common.Hash
	file, err := os.Open(m.mismatchFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn("Failed to open known footprint mismatches file for reading", "path", m.mismatchFile, "error", err)
		}
	} else {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" || line[0] == '#' {
				continue
			}
			entries = append(entries, common.HexToHash(line))
		}
		file.Close()
		if err := scanner.Err(); err != nil {
			log.Warn("Error reading known footprint mismatches file", "path", m.mismatchFile, "error", err)
		}
	}

	// Add new entry
	entries = append(entries, txHash)

	// Truncate if exceeding limit, keeping only the most recent entries
	if uint64(len(entries)) > m.maxMismatchEntries {
		entries = entries[len(entries)-int(m.maxMismatchEntries):]
		m.knownMismatches = make(map[common.Hash]bool)
		for _, h := range entries {
			m.knownMismatches[h] = true
		}
		log.Warn("Truncated known footprint mismatches file",
			"max_entries", m.maxMismatchEntries,
			"kept_entries", len(entries),
			"path", m.mismatchFile)
	}

	// Write all entries back to file
	if err := m.writeMismatchFile(entries); err != nil {
		log.Error("Failed to write known footprint mismatches file", "path", m.mismatchFile, "error", err)
		return err
	}

	log.Info("Recorded new footprint mismatch", "tx", txHash.Hex(), "path", m.mismatchFile)
	return nil
}

// It validates footprint strings to prevent DoS attacks via arbitrarily large payloads.
func (m *Manager) Store(txHash common.Hash, expectedFootprint, actualFootprint string, blockNumber uint64, mismatch bool) {
	if !isValidFootprint(expectedFootprint) {
		return
	}

	if !isValidFootprint(actualFootprint) {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.cache[txHash] = &Entry{
		ExpectedFootprint: expectedFootprint,
		ActualFootprint:   actualFootprint,
		BlockNumber:       blockNumber,
		Mismatch:          mismatch,
	}
}

// Get retrieves a footprint entry from the cache
func (m *Manager) Get(txHash common.Hash) (*Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.cache[txHash]
	return entry, ok
}

// EvictOldEntries removes cache entries older than maxCacheAge blocks from the current block
func (m *Manager) EvictOldEntries(currentBlockNumber uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if currentBlockNumber <= m.maxCacheAge {
		return
	}

	minBlockNumber := currentBlockNumber - m.maxCacheAge
	for txHash, entry := range m.cache {
		if entry.BlockNumber < minBlockNumber {
			delete(m.cache, txHash)
		}
	}
}

// GetStats returns statistics about the footprint manager
func (m *Manager) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mismatchCount := 0
	for _, entry := range m.cache {
		if entry.Mismatch {
			mismatchCount++
		}
	}

	return map[string]interface{}{
		"cache_size":                len(m.cache),
		"cache_mismatch_count":      mismatchCount,
		"known_mismatches_count":    len(m.knownMismatches),
		"max_cache_age_blocks":      m.maxCacheAge,
		"max_mismatch_entries":      m.maxMismatchEntries,
	}
}

// Clear removes all cache entries 
func (m *Manager) ClearCache() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cache = make(map[common.Hash]*Entry)
	log.Info("Footprint cache cleared")
}

// ShouldPanic checks the environment variable to determine if we should panic on mismatch.
func (m *Manager) ShouldPanic() bool {
	return os.Getenv("GETH_FOOTPRINT_PANIC") == "true"
}

// isValidFootprint validates that a footprint string is a valid fixed-length hex hash.
// A valid footprint must be:
//   - Empty string (allowed, skipped in processing)
//   - Exactly 64 hex characters (32 bytes) with optional "0x" prefix
//   - All characters must be valid hex digits (0-9, a-f, A-F)
func isValidFootprint(footprint string) bool {
	if footprint == "" {
		return true
	}

	hexPart := footprint
	if strings.HasPrefix(footprint, "0x") || strings.HasPrefix(footprint, "0X") {
		hexPart = footprint[2:]
	}

	if len(hexPart) != 64 {
		return false
	}

	for _, r := range hexPart {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}

	return true
}
