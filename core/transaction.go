package core

import (
	"github.com/google/uuid"
)

type Transaction struct {
	ID      string `json:"id"`
	Payload string `json:"payload"`
}

func NewTransaction(payload string) Transaction {
	return Transaction{
		ID:      uuid.NewString(),
		Payload: payload,
	}
}
