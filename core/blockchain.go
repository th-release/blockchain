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
	storage         *Storage
}

// NewBlockchain은 제네시스 블록이 포함된 새 체인을 생성합니다.
func NewBlockchain(host string) *Blockchain {
	storage, err := NewStorage("data", host)
	if err != nil {
		log.Fatalf("스토리지 초기화 실패: %v", err)
	}

	// 저장된 블록이 있으면 로드
	blocks, err := storage.LoadBlocks()
	if err != nil {
		log.Fatalf("블록 로드 실패: %v", err)
	}

	// 저장된 블록이 없으면 제네시스 블록 생성
	if len(blocks) == 0 {
		genesis := Block{
			Index:        0,
			Timestamp:    "2025-06-10T00:00:00Z",
			Transactions: []Transaction{},
			PrevHash:     "",
		}
		genesis.Hash = CalculateHash(genesis)
		blocks = []Block{genesis}

		// 제네시스 블록 저장
		if err := storage.SaveBlocks(blocks); err != nil {
			log.Fatalf("제네시스 블록 저장 실패: %v", err)
		}
	}

	return &Blockchain{
		blocks:          blocks,
		transactionPool: NewTransactionPool(),
		storage:         storage,
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

	// 블록 추가 전에 저장 시도
	bc.blocks = append(bc.blocks, newBlock)
	if err := bc.storage.SaveBlocks(bc.blocks); err != nil {
		// 저장 실패 시 블록 제거
		bc.blocks = bc.blocks[:len(bc.blocks)-1]
		log.Printf("블록 저장 실패로 추가 취소: %v", err)
		return Block{}
	}

	log.Printf("새 블록 추가 성공: 인덱스=%d, 해시=%s", newBlock.Index, newBlock.Hash)
	return newBlock
}

func (bc *Blockchain) MineBlock() Block {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	transactions := bc.transactionPool.GetTransactions()
	if len(transactions) == 0 {
		log.Println("마이닝 실패: 트랜잭션이 없음")
		return Block{}
	}

	lastBlock := bc.blocks[len(bc.blocks)-1]
	newBlock := GenerateBlock(lastBlock, transactions)

	if CalculateHash(newBlock) != newBlock.Hash {
		log.Printf("마이닝 실패: 해시 불일치. 계산된: %s, 생성된: %s", CalculateHash(newBlock), newBlock.Hash)
		return Block{}
	}

	// 블록 추가 전에 저장 시도
	bc.blocks = append(bc.blocks, newBlock)
	if err := bc.storage.SaveBlocks(bc.blocks); err != nil {
		// 저장 실패 시 블록 제거
		bc.blocks = bc.blocks[:len(bc.blocks)-1]
		log.Printf("블록 저장 실패로 마이닝 취소: %v", err)
		return Block{}
	}

	bc.transactionPool.Clear()
	log.Printf("블록 마이닝 성공: 인덱스=%d, 해시=%s", newBlock.Index, newBlock.Hash)
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
		log.Printf("[AppendBlock] 실패: 마지막 블록 해시(%s) != 새 블록 이전 해시(%s)", lastBlock.Hash, newBlock.PrevHash)
		return false
	}
	if CalculateHash(newBlock) != newBlock.Hash {
		log.Printf("[AppendBlock] 실패: 해시 불일치. 계산된: %s, 실제: %s", CalculateHash(newBlock), newBlock.Hash)
		return false
	}

	// 블록 추가 전에 저장 시도
	bc.blocks = append(bc.blocks, newBlock)
	if err := bc.storage.SaveBlocks(bc.blocks); err != nil {
		// 저장 실패 시 블록 제거
		bc.blocks = bc.blocks[:len(bc.blocks)-1]
		log.Printf("블록 저장 실패로 추가 취소: %v", err)
		return false
	}

	bc.transactionPool.Clear()
	log.Printf("새 블록 추가 성공: 인덱스=%d, 해시=%s", newBlock.Index, newBlock.Hash)
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
		log.Printf("체인 교체 실패: 새 체인 길이(%d) <= 현재 체인 길이(%d)", len(newBlocks), len(bc.blocks))
		return false
	}
	if !isValidChain(newBlocks) {
		log.Printf("체인 교체 실패: 유효하지 않은 체인")
		return false
	}

	// 체인 교체 전에 저장 시도
	oldBlocks := bc.blocks
	bc.blocks = newBlocks
	if err := bc.storage.SaveBlocks(bc.blocks); err != nil {
		// 저장 실패 시 원래 체인으로 복구
		bc.blocks = oldBlocks
		log.Printf("체인 저장 실패로 교체 취소: %v", err)
		return false
	}

	bc.transactionPool.Clear() // 체인 교체 시 트랜잭션 풀 비우기
	log.Printf("체인 교체 성공: 새 체인 길이=%d", len(newBlocks))
	return true
}
