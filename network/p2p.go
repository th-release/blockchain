package network

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cth-core.xyz/blockchain/core"
	"cth-core.xyz/blockchain/message"
	"cth-core.xyz/blockchain/pbft"
	util "cth-core.xyz/blockchain/util"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type P2PServer struct {
	bc           *core.Blockchain
	upgrader     websocket.Upgrader
	peers        map[string]*websocket.Conn
	peersMutex   sync.Mutex
	reconnecting map[string]bool // 재접속 시도 중인 주소 기록
	nodeID       string          // 현재 노드의 ID
	nodeIDs      []string        // 전체 네트워크 노드 ID
	pbftEngine   *pbft.PBFT      // 포인터로 선언
	mutex        sync.Mutex
	maxPeer      int
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
		bc:           bc,
		upgrader:     websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		peers:        make(map[string]*websocket.Conn),
		reconnecting: make(map[string]bool),
		nodeID:       nodeID.String(),
		nodeIDs:      []string{nodeID.String()}, // 처음엔 자기 자신만 포함
		maxPeer:      maxPeer,
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

	remoteAddr := util.NormalizeAddr(conn.RemoteAddr().String())

	s.peersMutex.Lock()

	if s.maxPeer < len(s.peers)+1 {
		s.peersMutex.Unlock()
		log.Println("Peer 최대 연결 갯수 한도초과로 연결 실패:", remoteAddr)
		closeMessage := websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Maximum peer connection limit exceeded")
		if err := conn.WriteControl(websocket.CloseMessage, closeMessage, time.Now().Add(time.Second)); err != nil {
			log.Printf("종료 메시지 전송 실패 (%s): %v", remoteAddr, err)
		}
		if err := conn.Close(); err != nil {
			log.Printf("WebSocket 연결 종료 실패 (%s): %v", remoteAddr, err)
		}
		return
	}
	s.peers[remoteAddr] = conn
	s.peersMutex.Unlock()

	// 연결되면 최신 블록 요청
	s.sendMessage(conn, message.Message{Type: message.QUERY_LATEST})

	// 1) 내 NodeID 전송해서 상대방에게 알려주기
	s.sendMessage(conn, message.Message{
		Type: message.AddNodeId,
		Data: mustMarshal(s.nodeID),
	})

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

func (s *P2PServer) onClose(conn *websocket.Conn, remoteAddr string, closeErr error) {
	// remoteAddr 정규화
	normalizedAddr := util.NormalizeAddr(remoteAddr)

	// 동시성 안전성을 위해 peersMutex 사용
	s.peersMutex.Lock()
	// 디버깅: 삭제 전 peers 맵 상태 출력
	log.Printf("삭제 전 peers 맵: %+v", s.peers)
	// normalizedAddr가 맵에 존재하는지 확인
	if _, exists := s.peers[normalizedAddr]; exists {
		delete(s.peers, normalizedAddr)
		log.Printf("피어 삭제 성공: %s", normalizedAddr)
	} else {
		log.Printf("피어 삭제 실패: %s는 peers 맵에 존재하지 않음", normalizedAddr)
	}
	s.peersMutex.Unlock()

	// 연결 종료 이유 로깅
	errMessage := ""
	closeReason := "정상 종료"
	if closeErr != nil {
		errMessage = closeErr.Error()
		if wsErr, ok := closeErr.(*websocket.CloseError); ok {
			closeReason = fmt.Sprintf("WebSocket 종료 코드: %d, 이유: %s", wsErr.Code, wsErr.Text)
		} else {
			closeReason = fmt.Sprintf("비정상 종료: %v", closeErr)
		}
		log.Printf("에러 메시지 출력: %s", errMessage)
	}

	// 연결이 이미 닫혔는지 확인하고 닫기
	if util.IsConnectionAlive(conn) {
		if err := conn.Close(); err != nil {
			log.Printf("WebSocket 연결 종료 실패: %v", err)
		}
	} else {
		log.Printf("연결 이미 닫힘: %s", normalizedAddr)
	}

	// 최종 피어 목록 로깅
	log.Printf("피어 연결 종료: %s, 종료 이유: %s, 현재 피어 목록: %+v", normalizedAddr, closeReason, s.GetPeers())
	log.Printf("========================= errMessage: %s", errMessage)
	log.Printf("========================= connection reset by peer: %v", strings.Contains(errMessage, "read: connection reset by peer"))
	log.Printf("========================= use of closed network connection: %v", strings.Contains(errMessage, "use of closed network connection"))

	// 비정상 종료가 아닌 경우에만 재접속 시도
	if !strings.Contains(errMessage, "read: connection reset by peer") &&
		!strings.Contains(errMessage, "use of closed network connection") &&
		!strings.Contains(errMessage, "Maximum peer connection limit exceeded") &&
		!util.IsConnectionAlive(conn) &&
		errMessage != "" {
		log.Printf("연결 종료 후 재접속 대기 시작: %s", normalizedAddr)
		s.tryReconnect(normalizedAddr)
	} else {
		log.Printf("비정상 종료로 인해 재접속 스킵: %s", normalizedAddr)
	}
}

func (s *P2PServer) handleMessages(conn *websocket.Conn) {
	remoteAddr := conn.RemoteAddr().String()
	defer func() {
		// defer에서 에러를 전달하기 위해 recover 또는 외부 에러 사용
		s.onClose(conn, remoteAddr, nil)
	}()

	for {
		var msg message.Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket 연결 종료 감지: %s, 에러: %v", remoteAddr, err)
				s.onClose(conn, remoteAddr, err)
				return
			}
			log.Println("메시지 수신 오류:", err)
			s.onClose(conn, remoteAddr, err)
			return
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
	s.peersMutex.Lock()
	defer s.peersMutex.Unlock()
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
	// blocks := []core.Block{block}
	// data, err := json.Marshal(blocks)
	// if err != nil {
	// 	fmt.Println("broadcast jasn parsing error")
	// 	// 에러 처리: 로그 출력 등
	// 	return
	// }

	if s.pbftEngine.IsLeader() {
		fmt.Println("전파 로직 수행")

		// s.Broadcast(Message{
		// 	Type: message.RESPONSE_BLOCKCHAIN,
		// 	Data: data,
		// })

		s.pbftEngine.Broadcast(message.MessageTypePBFTPrePrepare, block)
	} else {
		log.Println("리더 노드가 아니므로 블록 생성 생략")
	}

}

func (s *P2PServer) ConnectToPeer(address string) error {
	s.peersMutex.Lock()
	// 이미 연결된 피어인지 확인
	normalizedAddr := util.NormalizeAddr(address)
	if _, ok := s.peers[normalizedAddr]; ok {
		s.peersMutex.Unlock()
		log.Printf("이미 연결된 피어: %s", normalizedAddr)
		return nil
	}

	// 연결 시도 중임을 표시
	s.reconnecting[normalizedAddr] = true
	log.Printf("연결 시도 시작: %s, reconnecting 맵: %+v", normalizedAddr, s.reconnecting)

	s.peersMutex.Unlock()

	// 연결 시도 완료 후 reconnecting 상태 정리
	defer func() {
		s.peersMutex.Lock()
		delete(s.reconnecting, address)
		s.peersMutex.Unlock()
		log.Printf("연결 시도 완료, reconnecting 상태 해제: %s, reconnecting 맵: %+v", address, s.reconnecting)
	}()

	// 최대 피어 수 초과 체크
	s.peersMutex.Lock()
	if len(s.peers) >= s.maxPeer {
		s.peersMutex.Unlock()
		log.Printf("최대 피어 수 초과로 연결 실패: %s", address)
		return fmt.Errorf("maximum peer limit reached")
	}
	s.peersMutex.Unlock()

	url := "ws://" + address + "/ws"
	log.Printf("WebSocket 연결 시도: %s", url)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		log.Printf("피어 연결 실패 (%s): %v", address, err)
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

func (s *P2PServer) GetPeers() []string {
	s.peersMutex.Lock()
	defer s.peersMutex.Unlock()

	peers := make([]string, 0, len(s.peers))
	for addr := range s.peers {
		peers = append(peers, addr)
	}
	return peers
}

func (s *P2PServer) tryReconnect(address string) {
	normalizedAddr := util.NormalizeAddr(address)

	s.peersMutex.Lock()
	if s.reconnecting[normalizedAddr] {
		s.peersMutex.Unlock()
		log.Printf("이미 재접속 시도 중, 재접속 스킵: %s, reconnecting 맵: %+v", normalizedAddr, s.reconnecting)
		return
	}
	s.reconnecting[normalizedAddr] = true
	log.Printf("재접속 시도 시작: %s, reconnecting 맵: %+v", normalizedAddr, s.reconnecting)
	s.peersMutex.Unlock()

	defer func() {
		s.peersMutex.Lock()
		delete(s.reconnecting, normalizedAddr)
		s.peersMutex.Unlock()
		log.Printf("재접속 시도 종료, reconnecting 상태 해제: %s, reconnecting 맵: %+v", normalizedAddr, s.reconnecting)
	}()

	log.Printf("재접속 시도 전 대기: %s", normalizedAddr)
	time.Sleep(1 * time.Second)

	for attempt := 1; attempt <= 5; attempt++ {
		s.peersMutex.Lock()
		if _, ok := s.peers[normalizedAddr]; ok {
			s.peersMutex.Unlock()
			log.Printf("재접속 시도 중 연결 확인, 재접속 종료: %s", normalizedAddr)
			return
		}
		s.peersMutex.Unlock()

		log.Printf("재접속 시도 %d/%d: %s", attempt, 5, normalizedAddr)
		err := s.ConnectToPeer(address) // 원본 address 사용 (URL에 필요)
		if err == nil {
			log.Printf("피어 재접속 성공: %s", normalizedAddr)
			return
		}
		backoff := time.Duration(2<<uint(attempt-1)) * time.Second
		log.Printf("재접속 실패 (%s), %d초 후 재시도: %v", normalizedAddr, backoff/time.Second, err)
		time.Sleep(backoff)
	}
	log.Printf("재접속 최대 시도 횟수 초과, 종료: %s", normalizedAddr)
}

func (s *P2PServer) BroadcastTransaction(tx core.Transaction) {
	msg := message.Message{
		Type: message.MessageTypeTransaction,
		Data: mustMarshal(tx),
	}

	s.peersMutex.Lock()
	defer s.peersMutex.Unlock()

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
