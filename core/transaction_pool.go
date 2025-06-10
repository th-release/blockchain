package core

import (
	"log"
	"sync"
)

type TransactionPool struct {
	transactions []Transaction
	mu           sync.Mutex
}

func NewTransactionPool() *TransactionPool {
	return &TransactionPool{
		transactions: []Transaction{},
	}
}

func (tp *TransactionPool) Contains(tx Transaction) bool {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	for _, t := range tp.transactions {
		if t.ID == tx.ID {
			return true
		}
	}
	return false
}

func (tp *TransactionPool) AddTransaction(tx Transaction) bool {
	log.Println("AddTransaction: 락 획득 전")
	tp.mu.Lock()
	defer func() {
		tp.mu.Unlock()
		log.Println("AddTransaction: 락 해제됨")
	}()

	log.Println("AddTransaction: 락 획득됨, 중복 검사 시작")

	for _, t := range tp.transactions {
		if t.ID == tx.ID {
			log.Println("AddTransaction: 중복 트랜잭션 발견, 종료")
			return false
		}
	}

	tp.transactions = append(tp.transactions, tx)
	log.Println("AddTransaction: 트랜잭션 추가 완료")
	return true
}

func (tp *TransactionPool) GetTransactions() []Transaction {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	// 복사본 반환 (외부에서 변경 방지)
	txs := make([]Transaction, len(tp.transactions))
	copy(txs, tp.transactions)
	return txs
}

func (tp *TransactionPool) Clear() {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.transactions = []Transaction{}
}
