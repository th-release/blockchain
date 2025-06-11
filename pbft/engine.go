package pbft

import (
	"encoding/json"
	"log"
	"sync"

	"cth-core.xyz/blockchain/core"
	"cth-core.xyz/blockchain/message"
)

type PBFT struct {
	NodeID     string
	NodeIDs    []string              // 전체 노드 ID 목록
	View       int                   // 현재 라운드 번호
	BlockPool  map[string]core.Block // 블록 해시별로 저장
	prepareCnt map[string]int
	commitCnt  map[string]int
	mu         sync.Mutex

	Broadcast func(msgType int, block core.Block)
	AddBlock  func(block core.Block) bool

	preprepareReceived map[string]bool              // 블록 해시 기준 PrePrepare 도착 여부
	pendingPrepares    map[string][]message.Message // 대기 중인 Prepare 메시지
	pendingCommits     map[string][]message.Message // 대기 중인 Commit 메시지
}

func (p *PBFT) IsLeader() bool {
	if len(p.NodeIDs) == 0 {
		return false
	}
	leader := p.NodeIDs[p.View%len(p.NodeIDs)]
	return p.NodeID == leader
}

func (p *PBFT) UpdateNodeIDs(ids []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.NodeIDs = ids
}

func NewPBFT(nodeID string, broadcast func(int, core.Block), addBlock func(core.Block) bool) *PBFT {
	return &PBFT{
		NodeID:             nodeID,
		NodeIDs:            []string{nodeID},
		BlockPool:          make(map[string]core.Block),
		prepareCnt:         make(map[string]int),
		commitCnt:          make(map[string]int),
		Broadcast:          broadcast,
		AddBlock:           addBlock,
		preprepareReceived: make(map[string]bool),
		pendingPrepares:    make(map[string][]message.Message),
		pendingCommits:     make(map[string][]message.Message),
	}
}

type PBFTPayload struct {
	Block     core.Block `json:"block"`
	SenderID  string     `json:"sender_id"`
	Signature string     `json:"signature"` // 아직은 서명 없이 진행 가능
}

func (p *PBFT) OnPrePrepare(payload PBFTPayload) {
	p.mu.Lock()
	defer p.mu.Unlock()

	hash := payload.Block.Hash
	log.Printf("[PBFT] PrePrepare 수신: 블록=%+v, 해시=%s", payload.Block, hash)

	if _, exists := p.BlockPool[hash]; exists {
		log.Printf("[PBFT] 이미 처리된 블록 무시: %s", hash)
		return
	}

	if !isValidBlock(payload.Block) {
		log.Printf("[PBFT] 유효하지 않은 블록: %s", hash)
		return
	}

	p.BlockPool[hash] = payload.Block
	p.prepareCnt[hash] = 0
	p.commitCnt[hash] = 0
	p.preprepareReceived[hash] = true
	log.Printf("[PBFT] 블록 저장 완료: %s, BlockPool=%+v", hash, p.BlockPool)
	// 대기 중인 Prepare 메시지 처리
	if prepares, exists := p.pendingPrepares[hash]; exists {
		log.Printf("[PBFT] 처리할 대기 Prepare 메시지 수: %d", len(prepares))
		for _, msg := range prepares {
			var prepPayload PBFTPayload
			if err := json.Unmarshal(msg.Data, &prepPayload); err == nil {
				log.Printf("[PBFT] 대기 Prepare 처리: %s", prepPayload.Block.Hash)
				p.processPrepare(prepPayload)
			} else {
				log.Printf("[PBFT] 대기 Prepare 파싱 실패: %v", err)
			}
		}
		delete(p.pendingPrepares, hash)
	}

	// 대기 중인 Commit 메시지 처리
	if commits, exists := p.pendingCommits[hash]; exists {
		log.Printf("[PBFT] 대기 중인 Commit 메시지 처리 시작: %s", hash)
		for _, msg := range commits {
			var commitPayload PBFTPayload
			if err := json.Unmarshal(msg.Data, &commitPayload); err == nil {
				p.processCommit(commitPayload)
			}
		}
		delete(p.pendingCommits, hash)
	}

	// Prepare 메시지 브로드캐스트
	p.Broadcast(message.MessageTypePBFTPrepare, payload.Block)
	log.Printf("[PBFT] Prepare 메시지 브로드캐스트 완료: %s", hash)
}

func (p *PBFT) OnPrepare(payload PBFTPayload) {
	p.mu.Lock()
	defer p.mu.Unlock()

	hash := payload.Block.Hash
	log.Printf("[PBFT] Prepare 수신 시작: %s", hash)

	if !p.preprepareReceived[hash] {
		log.Printf("[PBFT] PrePrepare가 없는 Prepare 메시지 대기열에 추가: %s", hash)
		p.pendingPrepares[hash] = append(p.pendingPrepares[hash], message.Message{
			Type: message.MessageTypePBFTPrepare,
			Data: mustMarshal(payload),
		})
		return
	}

	p.processPrepare(payload)
}

func (p *PBFT) processPrepare(payload PBFTPayload) {
	hash := payload.Block.Hash
	if _, exists := p.BlockPool[hash]; !exists {
		log.Printf("[PBFT] 알 수 없는 블록 Prepare 무시: %s", hash)
		return
	}

	p.prepareCnt[hash]++
	f := (len(p.NodeIDs) - 1) / 3 // 결함 허용 노드 수
	required := 2*f + 1
	log.Printf("[PBFT] Prepare 처리: %d/%d, 해시=%s", p.prepareCnt[hash], required, hash)

	if p.prepareCnt[hash] >= required {
		log.Printf("[PBFT] 충분한 Prepare 수신, Commit 브로드캐스트: %s", hash)
		p.Broadcast(message.MessageTypePBFTCommit, payload.Block)
	}
}

func (p *PBFT) OnCommit(payload PBFTPayload) {
	p.mu.Lock()
	defer p.mu.Unlock()

	hash := payload.Block.Hash
	log.Printf("[PBFT] Commit 수신 시작: %s", hash)

	if !p.preprepareReceived[hash] {
		log.Printf("[PBFT] PrePrepare가 없는 Commit 메시지 대기열에 추가: %s", hash)
		p.pendingCommits[hash] = append(p.pendingCommits[hash], message.Message{
			Type: message.MessageTypePBFTCommit,
			Data: mustMarshal(payload),
		})
		return
	}

	p.processCommit(payload)
}

func (p *PBFT) processCommit(payload PBFTPayload) {
	hash := payload.Block.Hash
	p.commitCnt[hash]++

	f := (len(p.NodeIDs) - 1) / 3
	required := 2*f + 1

	if p.commitCnt[hash] >= required {
		log.Printf("[PBFT] 블록 최종 Commit 도달 → 체인에 추가 시도")

		success := p.AddBlock(payload.Block)
		if success {
			log.Printf("[PBFT] 블록 추가 성공: %s", payload.Block.Hash)
		} else {
			log.Printf("[PBFT] 블록 추가 실패: %s", payload.Block.Hash)
		}

		p.cleanupBlock(hash)
	}
}

func isValidBlock(block core.Block) bool {
	log.Printf("[isValidBlock] 검증 중인 블록: %+v", block)
	if block.Index < 0 {
		log.Printf("[isValidBlock] 잘못된 인덱스: %d", block.Index)
		return false
	}
	if block.Timestamp == "" {
		log.Printf("[isValidBlock] 빈 타임스탬프")
		return false
	}
	if block.Hash == "" {
		log.Printf("[isValidBlock] 빈 해시")
		return false
	}
	return true
}

func (p *PBFT) cleanupBlock(hash string) {
	delete(p.BlockPool, hash)
	delete(p.prepareCnt, hash)
	delete(p.commitCnt, hash)
	delete(p.preprepareReceived, hash)
	delete(p.pendingPrepares, hash)
	delete(p.pendingCommits, hash)
}

func mustMarshal(v interface{}) []byte {
	bytes, err := json.Marshal(v)
	if err != nil {
		log.Fatalf("Marshal 실패: %v", err)
	}
	return bytes
}
