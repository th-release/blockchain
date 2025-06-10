package core

import (
	"log"
	"sync"
)

// Blockchain 구조체는 블록의 리스트로 구성되며, 동시성을 위해 mutex를 포함합니다.
type Blockchain struct {
	blocks          []Block
	mu              sync.Mutex
	transactionPool *TransactionPool
}

// NewBlockchain은 제네시스 블록이 포함된 새 체인을 생성합니다.
func NewBlockchain() *Blockchain {
	genesis := Block{
		Index:        0,
		Timestamp:    "2025-06-10T00:00:00Z",
		Transactions: []Transaction{},
		PrevHash:     "",
	}
	genesis.Hash = CalculateHash(genesis)

	return &Blockchain{
		blocks:          []Block{genesis},
		transactionPool: NewTransactionPool(),
	}
}

func (bc *Blockchain) AddTransaction(tx Transaction) bool {
	if bc.transactionPool.AddTransaction(tx) {
		return true
	} else {
		return false
	}
}

// AddBlock은 체인에 새로운 블록을 추가합니다.
func (bc *Blockchain) AddBlock(transactions []Transaction) Block {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	lastBlock := bc.blocks[len(bc.blocks)-1]
	newBlock := GenerateBlock(lastBlock, transactions)
	bc.blocks = append(bc.blocks, newBlock)
	return newBlock
}

func (bc *Blockchain) MineBlock() Block {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	transactions := bc.transactionPool.GetTransactions()
	if len(transactions) == 0 {
		return Block{}
	}

	lastBlock := bc.blocks[len(bc.blocks)-1]
	newBlock := GenerateBlock(lastBlock, transactions)

	if CalculateHash(newBlock) != newBlock.Hash {
		return Block{}
	}

	bc.blocks = append(bc.blocks, newBlock)
	bc.transactionPool.Clear()
	return newBlock
}

// GetBlocks는 전체 체인을 반환합니다.
func (bc *Blockchain) GetBlocks() []Block {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return bc.blocks
}

// GetLastBlock은 마지막 블록을 반환합니다.
func (bc *Blockchain) GetLastBlock() Block {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return bc.blocks[len(bc.blocks)-1]
}

// IsValid는 체인의 유효성을 검사합니다.
func (bc *Blockchain) IsValid() bool {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	for i := 1; i < len(bc.blocks); i++ {
		prev := bc.blocks[i-1]
		curr := bc.blocks[i]

		if curr.PrevHash != prev.Hash {
			return false
		}

		if CalculateHash(curr) != curr.Hash {
			return false
		}
	}

	return true
}

func (bc *Blockchain) AppendBlock(newBlock Block) bool {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	lastBlock := bc.blocks[len(bc.blocks)-1]
	if lastBlock.Hash != newBlock.PrevHash {
		log.Printf("Append 실패: 마지막 해시 %s != 새 블록 이전 해시 %s\n", lastBlock.Hash, newBlock.PrevHash)
		return false
	}
	if CalculateHash(newBlock) != newBlock.Hash {
		log.Printf("Append 실패: 해시 불일치. 계산된: %s, 받은: %s\n", CalculateHash(newBlock), newBlock.Hash)
		return false
	}
	bc.blocks = append(bc.blocks, newBlock)
	bc.transactionPool.Clear()
	log.Println("새 블록 추가 성공")
	return true
}

func isValidChain(chain []Block) bool {
	if len(chain) == 0 {
		return false
	}
	if chain[0].Hash != CalculateHash(chain[0]) {
		return false
	}
	for i := 1; i < len(chain); i++ {
		if chain[i].PrevHash != chain[i-1].Hash {
			log.Println("chain[i].PrevHash != chain[i-1].Hash Error")
			return false
		}
		if CalculateHash(chain[i]) != chain[i].Hash {
			log.Println("CalculateHash(chain[i]) != chain[i].Hash Error")
			return false
		}
	}
	return true
}

// 체인 교체 (길고 유효한 체인만 교체)
func (bc *Blockchain) ReplaceChain(newBlocks []Block) bool {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if len(newBlocks) <= len(bc.blocks) {
		return false
	}
	if !isValidChain(newBlocks) {
		return false
	}
	bc.blocks = newBlocks
	bc.transactionPool.Clear() // 체인 교체 시 트랜잭션 풀 비우기
	return true
}
