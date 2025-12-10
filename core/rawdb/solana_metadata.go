package rawdb

import (
	"encoding/binary"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
)

var solanaMetadataPrefix = []byte("solana-meta-")

// solanaMetadataKey builds the database key for storing solana metadata for a block hash.
func solanaMetadataKey(blockHash common.Hash) []byte {
	key := make([]byte, len(solanaMetadataPrefix)+len(blockHash.Bytes()))
	copy(key, solanaMetadataPrefix)
	copy(key[len(solanaMetadataPrefix):], blockHash.Bytes())
	return key
}

// WriteSolanaMetadata stores the solana slot associated with a block hash.
func WriteSolanaMetadata(db ethdb.KeyValueWriter, blockHash common.Hash, slot uint64) {
	var enc [8]byte
	binary.BigEndian.PutUint64(enc[:], slot)
	db.Put(solanaMetadataKey(blockHash), enc[:])
}

// ReadSolanaMetadata retrieves the solana slot associated with a block hash.
func ReadSolanaMetadata(db ethdb.Reader, blockHash common.Hash) (uint64, bool) {
	data, err := db.Get(solanaMetadataKey(blockHash))
	if err != nil || len(data) < 8 {
		return 0, false
	}
	slot := binary.BigEndian.Uint64(data[:8])
	return slot, true
}
