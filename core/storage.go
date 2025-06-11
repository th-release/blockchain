package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

// Storage는 블록체인 데이터를 LevelDB에 저장하는 구조체입니다.
type Storage struct {
	db     *leveldb.DB
	dbPath string
	mu     sync.RWMutex
}

// NewStorage는 새로운 Storage 인스턴스를 생성합니다.
func NewStorage(dataDir string, host string) (*Storage, error) {
	// 절대 경로로 변환
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("데이터 디렉토리 경로 변환 실패: %v", err)
	}

	// LevelDB 경로 설정
	dbPath := filepath.Join(absDataDir, host+"_leveldb")

	// LevelDB 열기
	db, err := leveldb.OpenFile(dbPath, &opt.Options{
		Compression: opt.NoCompression, // 필요에 따라 SnappyCompression 사용 가능
	})
	if err != nil {
		return nil, fmt.Errorf("LevelDB 열기 실패: %v", err)
	}

	return &Storage{
		db:     db,
		dbPath: dbPath,
	}, nil
}

// SaveBlocks는 블록들을 LevelDB에 저장합니다.
func (s *Storage) SaveBlocks(blocks []Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 트랜잭션 시작
	batch := new(leveldb.Batch)

	// 블록 데이터를 직렬화하여 저장
	for i, block := range blocks {
		key := fmt.Sprintf("block:%d", i)
		data, err := json.Marshal(block)
		if err != nil {
			return fmt.Errorf("블록 %d 직렬화 실패: %v", i, err)
		}
		batch.Put([]byte(key), data)
	}

	// 배치 쓰기
	if err := s.db.Write(batch, nil); err != nil {
		return fmt.Errorf("LevelDB 쓰기 실패: %v", err)
	}

	return nil
}

// LoadBlocks는 LevelDB에서 블록들을 로드합니다.
func (s *Storage) LoadBlocks() ([]Block, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var blocks []Block
	iter := s.db.NewIterator(nil, nil)
	defer iter.Release()

	for iter.Next() {
		var block Block
		if err := json.Unmarshal(iter.Value(), &block); err != nil {
			return nil, fmt.Errorf("블록 역직렬화 실패: %v", err)
		}
		blocks = append(blocks, block)
	}

	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("이터레이터 에러: %v", err)
	}

	return blocks, nil
}

// GetBlock은 특정 인덱스의 블록을 조회합니다.
func (s *Storage) GetBlock(index int) (Block, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := fmt.Sprintf("block:%d", index)
	data, err := s.db.Get([]byte(key), nil)
	if err == leveldb.ErrNotFound {
		return Block{}, fmt.Errorf("블록 %d 없음", index)
	}
	if err != nil {
		return Block{}, fmt.Errorf("LevelDB 읽기 실패: %v", err)
	}

	var block Block
	if err := json.Unmarshal(data, &block); err != nil {
		return Block{}, fmt.Errorf("블록 역직렬화 실패: %v", err)
	}
	return block, nil
}

// Close는 LevelDB 연결을 닫습니다.
func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.db.Close(); err != nil {
		return fmt.Errorf("LevelDB 닫기 실패: %v", err)
	}
	return nil
}
