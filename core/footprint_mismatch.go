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

package core

import (
	"bufio"
	"os"
	"path/filepath"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

// FootprintMismatchTracker tracks known footprint mismatches to allow
// indexing to continue past pre-fix transactions while still detecting new issues.
type FootprintMismatchTracker struct {
	mu            sync.RWMutex
	knownMismatches map[common.Hash]bool
	filePath      string
}

var (
	globalMismatchTracker     *FootprintMismatchTracker
	globalMismatchTrackerOnce sync.Once
)

// GetFootPrintMismatchTracker returns the global FootprintMismatchTracker singleton.
func GetFootPrintMismatchTracker(dataDir string) *FootprintMismatchTracker {
	globalMismatchTrackerOnce.Do(func() {
		filePath := filepath.Join(dataDir, "known_footprint_mismatches.txt")
		globalMismatchTracker = &FootprintMismatchTracker{
			knownMismatches: make(map[common.Hash]bool),
			filePath:        filePath,
		}
		globalMismatchTracker.load()
	})
	return globalMismatchTracker
}

// load reads known mismatch tx hashes from disk
func (t *FootprintMismatchTracker) load() {
	t.mu.Lock()
	defer t.mu.Unlock()

	file, err := os.Open(t.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Info("No known footprint mismatches file found, starting fresh", "path", t.filePath)
			return
		}
		log.Warn("Failed to open known footprint mismatches file", "path", t.filePath, "error", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' {
			continue 
		}
		txHash := common.HexToHash(line)
		t.knownMismatches[txHash] = true
		count++
	}

	if err := scanner.Err(); err != nil {
		log.Warn("Error reading known footprint mismatches file", "path", t.filePath, "error", err)
		return
	}

	log.Info("Loaded known footprint mismatches", "count", count, "path", t.filePath)
}

// IsKnown checks if a transaction hash is in the known mismatches list
func (t *FootprintMismatchTracker) IsKnown(txHash common.Hash) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.knownMismatches[txHash]
}

// RecordMismatch adds a new mismatch to the known list and persists to disk
func (t *FootprintMismatchTracker) RecordMismatch(txHash common.Hash) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if already known
	if t.knownMismatches[txHash] {
		return nil
	}

	// Add to in-memory map
	t.knownMismatches[txHash] = true

	// Append to file
	file, err := os.OpenFile(t.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Error("Failed to open known footprint mismatches file for writing", "path", t.filePath, "error", err)
		return err
	}
	defer file.Close()

	if _, err := file.WriteString(txHash.Hex() + "\n"); err != nil {
		log.Error("Failed to write to known footprint mismatches file", "path", t.filePath, "error", err)
		return err
	}

	log.Info("Recorded new footprint mismatch", "tx", txHash.Hex(), "path", t.filePath)
	return nil
}

// ShouldPanic checks the environment variable to determine if we should panic on mismatch.
func (t *FootprintMismatchTracker) ShouldPanic() bool {
	return os.Getenv("GETH_FOOTPRINT_PANIC") != "true"
}
