package pbft

import (
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
		NodeID:     nodeID,
		NodeIDs:    []string{nodeID},
		BlockPool:  make(map[string]core.Block),
		prepareCnt: make(map[string]int),
		commitCnt:  make(map[string]int),
		Broadcast:  broadcast,
		AddBlock:   addBlock,
	}
}

type PBFTPayload struct {
	Block     core.Block `json:"block"`
	SenderID  string     `json:"sender_id"`
	Signature string     `json:"signature"` // 아직은 서명 없이 진행 가능
}

func (p *PBFT) OnPrepare(payload PBFTPayload) {
	p.mu.Lock()
	defer p.mu.Unlock()

	hash := payload.Block.Hash

	if _, exists := p.BlockPool[hash]; !exists {
		log.Println("[PBFT] 알 수 없는 블록 Prepare 무시")
		return
	}

	p.prepareCnt[hash]++
	log.Printf("[PBFT] Prepare 수신 (%d/%d)", p.prepareCnt[hash], 2)

	// 예시: 2명 이상 Prepare 시 Commit 전송
	if p.prepareCnt[hash] == 2 {
		p.Broadcast(message.MessageTypePBFTCommit, payload.Block)
	}
}

func (p *PBFT) OnPrePrepare(payload PBFTPayload) {
	p.mu.Lock()
	defer p.mu.Unlock()

	hash := payload.Block.Hash

	// 이미 본 블록이면 무시
	if _, exists := p.BlockPool[hash]; exists {
		return
	}

	p.BlockPool[hash] = payload.Block
	p.prepareCnt[hash] = 0
	p.commitCnt[hash] = 0

	log.Println("[PBFT] PrePrepare 수신, Prepare 브로드캐스트 시작")

	p.Broadcast(message.MessageTypePBFTPrepare, payload.Block)
}

func (p *PBFT) OnCommit(payload PBFTPayload) {
	p.mu.Lock()
	defer p.mu.Unlock()

	hash := payload.Block.Hash

	if _, exists := p.BlockPool[hash]; !exists {
		log.Println("[PBFT] 알 수 없는 블록 Commit 무시")
		return
	}

	p.commitCnt[hash]++
	log.Printf("[PBFT] Commit 수신 (%d/%d)", p.commitCnt[hash], 2)

	if p.commitCnt[hash] == 2 {
		success := p.AddBlock(payload.Block)
		if success {
			log.Println("[PBFT] 블록 최종 확정 및 체인에 추가")
		} else {
			log.Println("[PBFT] 블록 추가 실패 (중복?)")
		}
		// 정리
		delete(p.BlockPool, hash)
		delete(p.prepareCnt, hash)
		delete(p.commitCnt, hash)
	}
}
