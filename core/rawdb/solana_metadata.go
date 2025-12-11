package rawdb

import (
	"encoding/binary"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
)

var solanaTxMetadataPrefix = []byte("solana-tx-meta-")

// solanaTxMetadataKey builds the database key for storing solana metadata for a transaction hash.
func solanaTxMetadataKey(txHash common.Hash) []byte {
	key := make([]byte, len(solanaTxMetadataPrefix)+len(txHash.Bytes()))
	copy(key, solanaTxMetadataPrefix)
	copy(key[len(solanaTxMetadataPrefix):], txHash.Bytes())
	return key
}

// WriteSolanaTxMetadata stores the solana slot and timestamp associated with a transaction hash.
func WriteSolanaTxMetadata(db ethdb.KeyValueWriter, txHash common.Hash, slot uint64, timestamp int64) {
	var enc [16]byte
	binary.BigEndian.PutUint64(enc[:8], slot)
	binary.BigEndian.PutUint64(enc[8:], uint64(timestamp))
	db.Put(solanaTxMetadataKey(txHash), enc[:])
}
