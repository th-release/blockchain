package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Storage는 블록체인 데이터를 JSON 파일로 저장하는 구조체입니다.
type Storage struct {
	filePath string
	mu       sync.RWMutex
}

// NewStorage는 새로운 Storage 인스턴스를 생성합니다.
func NewStorage(dataDir string, host string) (*Storage, error) {
	// 데이터 디렉토리가 없으면 생성
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	filePath := filepath.Join(dataDir, host+".json")
	return &Storage{
		filePath: filePath,
	}, nil
}

// SaveBlocks는 블록들을 JSON 파일로 저장합니다.
func (s *Storage) SaveBlocks(blocks []Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(blocks, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.filePath, data, 0644)
}

// LoadBlocks는 JSON 파일에서 블록들을 로드합니다.
func (s *Storage) LoadBlocks() ([]Block, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Block{}, nil
		}
		return nil, err
	}

	var blocks []Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil, err
	}

	return blocks, nil
}
