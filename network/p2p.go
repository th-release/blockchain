package network

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"cth-core.xyz/blockchain/core"
	"cth-core.xyz/blockchain/message"
	"cth-core.xyz/blockchain/pbft"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type P2PServer struct {
	bc             *core.Blockchain
	upgrader       websocket.Upgrader
	peers          map[string]*websocket.Conn
	peersMutex     sync.RWMutex
	reconnectState map[string]*ReconnectState // 재접속 상태 관리
	nodeID         string                     // 현재 노드의 ID
	nodeIDs        []string                   // 전체 네트워크 노드 ID
	pbftEngine     *pbft.PBFT                 // 포인터로 선언
	mutex          sync.Mutex
	maxPeer        int
}

// 재접속 상태 구조체
type ReconnectState struct {
	isReconnecting bool
	lastAttempt    time.Time
	attemptCount   int
	maxAttempts    int
	mutex          sync.Mutex
}

func (s *P2PServer) Info() *pbft.PBFT {
	return s.pbftEngine
}

func NewP2PServer(bc *core.Blockchain, maxPeer int) *P2PServer {
	// nodeID 자동 생성
	nodeID, err := uuid.NewUUID()
	if err != nil {
		log.Fatalf("UUID 생성 실패: %v", err)
	}

	s := &P2PServer{
		bc:             bc,
		upgrader:       websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		peers:          make(map[string]*websocket.Conn),
		reconnectState: make(map[string]*ReconnectState),
		nodeID:         nodeID.String(),
		nodeIDs:        []string{nodeID.String()}, // 처음엔 자기 자신만 포함
		maxPeer:        maxPeer,
	}

	// pbftEngine 초기화
	s.pbftEngine = pbft.NewPBFT(s.nodeID, s.BroadcastPBFTMessage, bc.AppendBlock)

	return s
}

func (s *P2PServer) BroadcastPBFTMessage(msgType int, block core.Block) {
	payload := pbft.PBFTPayload{
		Block:    block,
		SenderID: s.pbftEngine.NodeID,
		// Signature 필드는 현재 생략 가능
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Println("PBFT 메시지 직렬화 실패:", err)
		return
	}

	msg := message.Message{
		Type: msgType,
		Data: data,
	}

	s.Broadcast(msg)
}

func (s *P2PServer) HandleConnections(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket 업그레이드 실패:", err)
		return
	}

	remoteAddr := conn.RemoteAddr().String()

	s.peersMutex.Lock()

	// 최대 피어 수 체크
	if len(s.peers) >= s.maxPeer {
		s.peersMutex.Unlock()
		log.Println("Peer 최대 연결 갯수 한도초과로 연결 실패:", remoteAddr)
		conn.Close()
		return
	}

	s.peers[remoteAddr] = conn

	// 재접속 상태 초기화 (성공적으로 연결됨)
	if state, exists := s.reconnectState[remoteAddr]; exists {
		state.mutex.Lock()
		state.isReconnecting = false
		state.attemptCount = 0
		state.mutex.Unlock()
	}

	s.peersMutex.Unlock()

	log.Println("새 피어 연결:", remoteAddr)

	// 연결되면 최신 블록 요청
	s.sendMessage(conn, message.Message{Type: message.QUERY_LATEST})

	// 1) 내 NodeID 전송해서 상대방에게 알려주기
	s.sendMessage(conn, message.Message{
		Type: message.AddNodeId,
		Data: mustMarshal(s.nodeID),
	})

	log.Println(s.GetPeers())

	s.sendMessage(conn, message.Message{
		Type: message.AddPeers,
		Data: mustMarshal(s.GetPeers()),
	})

	go s.handleMessages(conn)
}

func (s *P2PServer) AddNodeId(id string) {
	for _, existing := range s.nodeIDs {
		if existing == id {
			return
		}
	}
	s.nodeIDs = append(s.nodeIDs, id)
	log.Println("노드 ID 등록됨:", id)

	if s.pbftEngine != nil {
		s.pbftEngine.UpdateNodeIDs(s.nodeIDs)
	}
}

func (s *P2PServer) clearReconnectState(address string) {
	s.peersMutex.Lock()
	defer s.peersMutex.Unlock()

	if state, exists := s.reconnectState[address]; exists {
		state.mutex.Lock()
		state.isReconnecting = false
		state.attemptCount = 0
		state.lastAttempt = time.Time{}
		state.mutex.Unlock()
		log.Printf("reconnectingState 정리됨: %s", address)
	}
}

func (s *P2PServer) handleMessages(conn *websocket.Conn) {
	remoteAddr := conn.RemoteAddr().String()
	defer func() {
		s.peersMutex.Lock()
		delete(s.peers, remoteAddr)
		s.peersMutex.Unlock()
		conn.Close()
		log.Printf("피어 연결 종료: %s, 현재 피어 목록: %+v", remoteAddr, s.GetPeers())

		// 메시지 처리가 완전히 끝났으므로 reconnectingState 정리
		s.clearReconnectState(remoteAddr)

		// 재접속 시도 (백그라운드에서)
		go s.scheduleReconnect(remoteAddr)
	}()

	for {
		var msg message.Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			log.Println("메시지 수신 오류:", err)
			break
		}

		switch msg.Type {
		case message.QUERY_LATEST:
			latestBlock := s.bc.GetLastBlock()
			data, _ := json.Marshal([]core.Block{latestBlock})
			s.sendMessage(conn, message.Message{Type: message.RESPONSE_BLOCKCHAIN, Data: data})

		case message.QUERY_ALL:
			blocks := s.bc.GetBlocks()
			data, _ := json.Marshal(blocks)
			s.sendMessage(conn, message.Message{Type: message.RESPONSE_BLOCKCHAIN, Data: data})

		case message.RESPONSE_BLOCKCHAIN:
			s.handleBlockchainResponse(msg.Data)

		case message.MessageTypeTransaction:
			var tx core.Transaction
			if err := json.Unmarshal(msg.Data, &tx); err != nil {
				log.Println("트랜잭션 언마샬 실패:", err)
				continue
			}

			if s.bc.AddTransaction(tx) {
				log.Println("트랜잭션 수신 및 pool에 추가:", tx.ID)
			} else {
				log.Println("중복 트랜잭션 혹은 무시됨:", tx.ID)
			}

		case message.MessageTypePBFTPrePrepare:
			var pbft pbft.PBFTPayload
			if err := json.Unmarshal(msg.Data, &pbft); err != nil {
				log.Println("PrePrepare 메시지 파싱 실패:", err)
				break
			}
			s.pbftEngine.OnPrePrepare(pbft)

		case message.MessageTypePBFTPrepare:
			var pbft pbft.PBFTPayload
			if err := json.Unmarshal(msg.Data, &pbft); err != nil {
				log.Println("Prepare 메시지 파싱 실패:", err)
				break
			}
			s.pbftEngine.OnPrepare(pbft)

		case message.MessageTypePBFTCommit:
			var pbft pbft.PBFTPayload
			if err := json.Unmarshal(msg.Data, &pbft); err != nil {
				log.Println("Commit 메시지 파싱 실패:", err)
				break
			}
			s.pbftEngine.OnCommit(pbft)

		case message.AddNodeId:
			var peerID string
			if err := json.Unmarshal(msg.Data, &peerID); err == nil {
				log.Println("상대방 NodeID:", peerID)
				s.AddNodeId(peerID)
			}

		case message.AddPeers:
			var peerAddresses []string
			log.Println(msg.Data)
			if err := json.Unmarshal(msg.Data, &peerAddresses); err != nil {
				log.Println(peerAddresses)
				log.Println("피어 정보 역직렬화 실패:", err)
				break
			}
			log.Println("상대방 Peers:", peerAddresses)
			for _, addr := range peerAddresses {
				if err := s.ConnectToPeer(addr); err != nil {
					log.Printf("피어 연결 실패 (%s): %v", addr, err)
				} else {
					log.Printf("피어 연결 성공: %s", addr)
				}
			}
		}
	}
}

func (s *P2PServer) handleBlockchainResponse(data json.RawMessage) {
	fmt.Println("handleBlockchainResponse!")
	var receivedBlocks []core.Block
	if err := json.Unmarshal(data, &receivedBlocks); err != nil {
		log.Println("블록체인 데이터 언마샬 오류:", err)
		return
	}
	if len(receivedBlocks) == 0 {
		log.Println("빈 블록체인 데이터 수신")
		return
	}

	latestReceivedBlock := receivedBlocks[len(receivedBlocks)-1]
	latestLocalBlock := s.bc.GetLastBlock()

	if latestReceivedBlock.Index > latestLocalBlock.Index {
		log.Printf("수신 블록 높음: %d (내 블록 높이 %d)", latestReceivedBlock.Index, latestLocalBlock.Index)

		if latestLocalBlock.Hash == latestReceivedBlock.PrevHash {
			s.bc.AppendBlock(latestReceivedBlock)
			s.Broadcast(message.Message{Type: message.RESPONSE_BLOCKCHAIN, Data: data})
		} else if len(receivedBlocks) == 1 {
			s.broadcastMessage(message.Message{Type: message.QUERY_ALL})
		} else {
			if s.bc.ReplaceChain(receivedBlocks) {
				log.Println("내 체인 전체 교체 성공")
				s.Broadcast(message.Message{Type: message.RESPONSE_BLOCKCHAIN, Data: data})
			} else {
				log.Println("내 체인 전체 교체 실패: 유효하지 않은 체인")
			}
		}
	} else {
		log.Println("수신 블록이 내 체인보다 낮거나 같음")
	}
}

func (s *P2PServer) sendMessage(conn *websocket.Conn, msg message.Message) error {
	return conn.WriteJSON(msg)
}

func (s *P2PServer) Broadcast(msg message.Message) {
	s.peersMutex.RLock()
	defer s.peersMutex.RUnlock()
	for _, peer := range s.peers {
		fmt.Printf("전파! %s\n", msg.Data)
		peer.WriteJSON(msg)
	}
}

func (s *P2PServer) broadcastMessage(msg message.Message) {
	s.Broadcast(msg)
}

// 피어에 새 블록 전파
func (s *P2PServer) BroadcastNewBlock(block core.Block) {
	if s.pbftEngine.IsLeader() {
		fmt.Println("전파 로직 수행")
		s.pbftEngine.Broadcast(message.MessageTypePBFTPrePrepare, block)
	} else {
		log.Println("리더 노드가 아니므로 블록 생성 생략")
	}
}

func (s *P2PServer) ConnectToPeer(address string) error {
	s.peersMutex.Lock()
	// 이미 연결된 피어인지 확인
	if _, ok := s.peers[address]; ok {
		s.peersMutex.Unlock()
		log.Printf("이미 연결된 피어: %s", address)
		return nil
	}
	s.peersMutex.Unlock()

	// 재접속 상태 확인
	if !s.canAttemptConnection(address) {
		return fmt.Errorf("connection attempt blocked for %s", address)
	}

	// 최대 피어 수 초과 체크
	s.peersMutex.RLock()
	if len(s.peers) >= s.maxPeer {
		s.peersMutex.RUnlock()
		log.Printf("최대 피어 수 초과로 연결 실패: %s", address)
		return fmt.Errorf("maximum peer limit reached")
	}
	s.peersMutex.RUnlock()

	url := "ws://" + address + "/ws"
	log.Printf("WebSocket 연결 시도: %s", url)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		log.Printf("피어 연결 실패 (%s): %v", address, err)
		s.markConnectionFailure(address)
		return err
	}

	s.peersMutex.Lock()
	s.peers[address] = conn
	s.peersMutex.Unlock()

	log.Printf("새 피어에 연결됨: %s, 현재 피어 목록: %+v", address, s.GetPeers())

	s.sendMessage(conn, message.Message{Type: message.QUERY_LATEST})

	go s.handleMessages(conn)

	return nil
}

// 연결 시도 가능 여부 확인
func (s *P2PServer) canAttemptConnection(address string) bool {
	s.peersMutex.Lock()
	defer s.peersMutex.Unlock()

	state, exists := s.reconnectState[address]
	if !exists {
		s.reconnectState[address] = &ReconnectState{
			isReconnecting: false,
			maxAttempts:    5,
		}
		return true
	}

	state.mutex.Lock()
	defer state.mutex.Unlock()

	// 이미 재접속 시도 중이면 차단
	if state.isReconnecting {
		log.Printf("재접속 시도 중인 피어, 연결 스킵: %s", address)
		return false
	}

	// 최대 시도 횟수 초과 체크
	if state.attemptCount >= state.maxAttempts {
		// 마지막 시도로부터 10분이 지났다면 초기화
		if time.Since(state.lastAttempt) > 10*time.Minute {
			state.attemptCount = 0
			log.Printf("재접속 제한 시간 만료, 시도 횟수 초기화: %s", address)
		} else {
			log.Printf("최대 재접속 시도 횟수 초과: %s", address)
			return false
		}
	}

	state.isReconnecting = true
	return true
}

// 연결 실패 표시
func (s *P2PServer) markConnectionFailure(address string) {
	s.peersMutex.Lock()
	defer s.peersMutex.Unlock()

	if state, exists := s.reconnectState[address]; exists {
		state.mutex.Lock()
		state.isReconnecting = false
		state.attemptCount++
		state.lastAttempt = time.Now()
		state.mutex.Unlock()
	}
}

// 연결 성공 표시
func (s *P2PServer) markConnectionSuccess(address string) {
	s.peersMutex.Lock()
	defer s.peersMutex.Unlock()

	if state, exists := s.reconnectState[address]; exists {
		state.mutex.Lock()
		state.isReconnecting = false
		state.attemptCount = 0
		state.lastAttempt = time.Time{}
		state.mutex.Unlock()
	}
}

func (s *P2PServer) GetPeers() []string {
	s.peersMutex.RLock()
	defer s.peersMutex.RUnlock()

	peers := make([]string, 0, len(s.peers))
	for addr := range s.peers {
		peers = append(peers, addr)
	}
	return peers
}

// 재접속 스케줄링 (단순화된 버전)
func (s *P2PServer) scheduleReconnect(address string) {
	// 초기 대기 시간
	time.Sleep(5 * time.Second)

	for attempt := 1; attempt <= 5; attempt++ {
		// 이미 연결된 경우 중단
		s.peersMutex.RLock()
		if _, connected := s.peers[address]; connected {
			s.peersMutex.RUnlock()
			log.Printf("재접속 시도 중 연결 확인됨, 중단: %s", address)
			return
		}
		s.peersMutex.RUnlock()

		log.Printf("재접속 시도 %d/%d: %s", attempt, 5, address)

		err := s.ConnectToPeer(address)
		if err == nil {
			log.Printf("피어 재접속 성공: %s", address)
			return
		}

		// 지수 백오프 적용
		backoff := time.Duration(5<<uint(attempt-1)) * time.Second
		if backoff > 10*time.Second {
			backoff = 10 * time.Second
		}

		log.Printf("재접속 실패 (%s), %d초 후 재시도: %v", address, backoff/time.Second, err)
		time.Sleep(backoff)
	}

	log.Printf("재접속 최대 시도 횟수 초과, 종료: %s", address)
}

func (s *P2PServer) BroadcastTransaction(tx core.Transaction) {
	msg := message.Message{
		Type: message.MessageTypeTransaction,
		Data: mustMarshal(tx),
	}

	s.peersMutex.RLock()
	defer s.peersMutex.RUnlock()

	for _, peer := range s.peers {
		if err := peer.WriteJSON(msg); err != nil {
			log.Println("트랜잭션 전송 실패:", err)
		}
	}
}

func mustMarshal(v interface{}) json.RawMessage {
	bytes, err := json.Marshal(v)
	if err != nil {
		log.Fatalf("Marshal 실패: %v", err)
	}
	return bytes
}
