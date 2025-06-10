package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"
)

// Block은 블록체인에 저장되는 단일 블록 구조입니다.
type Block struct {
	Index        int           `json:"index"`
	Timestamp    string        `json:"timestamp"`
	Transactions []Transaction `json:"transactions"`
	PrevHash     string        `json:"prev_hash"`
	Hash         string        `json:"hash"`
}

// CalculateHash는 블록 내용을 바탕으로 해시를 계산합니다.
func CalculateHash(block Block) string {
	txBytes, _ := json.Marshal(block.Transactions)
	record := strconv.Itoa(block.Index) + block.Timestamp + string(txBytes) + block.PrevHash
	h := sha256.New()
	h.Write([]byte(record))
	hashed := h.Sum(nil)
	return hex.EncodeToString(hashed)
}

func GenerateBlock(prevBlock Block, transactions []Transaction) Block {
	newBlock := Block{
		Index:        prevBlock.Index + 1,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Transactions: transactions,
		PrevHash:     prevBlock.Hash,
	}
	newBlock.Hash = CalculateHash(newBlock)
	return newBlock
}
