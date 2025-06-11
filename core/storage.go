package core

import (
	"encoding/json"
	"fmt"
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
	// 절대 경로로 변환
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("데이터 디렉토리 경로 변환 실패: %v", err)
	}

	// 데이터 디렉토리가 없으면 생성
	if err := os.MkdirAll(absDataDir, 0755); err != nil {
		return nil, fmt.Errorf("데이터 디렉토리 생성 실패: %v", err)
	}

	filePath := filepath.Join(absDataDir, host+".json")
	return &Storage{
		filePath: filePath,
	}, nil
}

// SaveBlocks는 블록들을 JSON 파일로 저장합니다.
func (s *Storage) SaveBlocks(blocks []Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 임시 파일에 먼저 쓰기
	tempPath := s.filePath + ".tmp"

	// 블록 데이터 직렬화
	data, err := json.MarshalIndent(blocks, "", "  ")
	if err != nil {
		return fmt.Errorf("블록 직렬화 실패: %v", err)
	}

	// 임시 파일에 쓰기
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("임시 파일 쓰기 실패: %v", err)
	}

	// 파일 시스템 동기화
	if err := syncFile(tempPath); err != nil {
		os.Remove(tempPath) // 임시 파일 정리
		return fmt.Errorf("파일 동기화 실패: %v", err)
	}

	// 원자적 파일 교체
	if err := os.Rename(tempPath, s.filePath); err != nil {
		os.Remove(tempPath) // 임시 파일 정리
		return fmt.Errorf("파일 교체 실패: %v", err)
	}

	// 최종 파일 동기화
	if err := syncFile(s.filePath); err != nil {
		return fmt.Errorf("최종 파일 동기화 실패: %v", err)
	}

	return nil
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
		return nil, fmt.Errorf("파일 읽기 실패: %v", err)
	}

	var blocks []Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil, fmt.Errorf("블록 역직렬화 실패: %v", err)
	}

	return blocks, nil
}

// syncFile은 파일 시스템 동기화를 수행합니다.
func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// 파일 디스크립터 동기화
	if err := file.Sync(); err != nil {
		return err
	}

	// 디렉토리 동기화
	dir, err := os.OpenFile(filepath.Dir(path), os.O_RDONLY, 0755)
	if err != nil {
		return err
	}
	defer dir.Close()

	return dir.Sync()
}
