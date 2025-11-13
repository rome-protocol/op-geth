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

// WriteSolanaMetadata stores the solana slot and hash associated with a block hash.
func WriteSolanaMetadata(db ethdb.KeyValueWriter, blockHash common.Hash, slot uint64, solanaHash common.Hash) {
	var enc [8 + common.HashLength]byte
	binary.BigEndian.PutUint64(enc[:8], slot)
	copy(enc[8:], solanaHash.Bytes())
	db.Put(solanaMetadataKey(blockHash), enc[:])
}

// ReadSolanaMetadata retrieves the solana slot and hash associated with a block hash.
func ReadSolanaMetadata(db ethdb.Reader, blockHash common.Hash) (uint64, common.Hash, bool) {
	data, err := db.Get(solanaMetadataKey(blockHash))
	if err != nil || len(data) < 8 {
		return 0, common.Hash{}, false
	}
	slot := binary.BigEndian.Uint64(data[:8])
	var solanaHash common.Hash
	if len(data) >= 8+common.HashLength {
		copy(solanaHash[:], data[8:8+common.HashLength])
	}
	return slot, solanaHash, true
}
