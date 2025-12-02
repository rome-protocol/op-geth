package core

import (
	"container/list"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

type solanaMetadataEntry struct {
	blockHash  common.Hash
	slot       uint64
	solanaHash common.Hash
}

type solanaMetadataCache struct {
	mu       sync.RWMutex
	capacity int
	ll       *list.List
	cache    map[common.Hash]*list.Element
}

func newSolanaMetadataCache(capacity int) *solanaMetadataCache {
	return &solanaMetadataCache{
		capacity: capacity,
		ll:       list.New(),
		cache:    make(map[common.Hash]*list.Element),
	}
}

func (c *solanaMetadataCache) Get(blockHash common.Hash) (uint64, common.Hash, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if elem, ok := c.cache[blockHash]; ok {
		entry := elem.Value.(*solanaMetadataEntry)
		return entry.slot, entry.solanaHash, true
	}
	return 0, common.Hash{}, false
}

func (c *solanaMetadataCache) Add(blockHash common.Hash, slot uint64, solanaHash common.Hash) {
	if c == nil || c.capacity <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.cache[blockHash]; ok {
		entry := elem.Value.(*solanaMetadataEntry)
		entry.slot = slot
		entry.solanaHash = solanaHash
		c.ll.MoveToFront(elem)
		return
	}

	entry := &solanaMetadataEntry{
		blockHash:  blockHash,
		slot:       slot,
		solanaHash: solanaHash,
	}
	elem := c.ll.PushFront(entry)
	c.cache[blockHash] = elem

	for c.ll.Len() > c.capacity {
		back := c.ll.Back()
		if back == nil {
			break
		}
		c.ll.Remove(back)
		if e, ok := back.Value.(*solanaMetadataEntry); ok {
			delete(c.cache, e.blockHash)
		}
	}
}
