package network

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"cth-core.xyz/blockchain/core"
	"cth-core.xyz/blockchain/message"
	"cth-core.xyz/blockchain/pbft"
	util "cth-core.xyz/blockchain/util"

	"github.com/google/uuid"
)

type P2PServer struct {
	bc                *core.Blockchain
	peers             map[string]*net.TCPConn // TCP 연결로 변경
	peersMutex        sync.Mutex
	reconnecting      map[string]bool
	seenMessages      map[string]bool // 메시지 중복 방지
	seenMessagesMutex sync.Mutex
	nodeID            string
	nodeIDs           []string
	pbftEngine        *pbft.PBFT
	mutex             sync.Mutex
	maxPeer           int
	gossipFanout      int // Gossip 전파 시 선택할 피어 수
	listener          *net.TCPListener
}

func (s *P2PServer) Info() *pbft.PBFT {
	return s.pbftEngine
}

// StartScheduler starts a scheduler that processes transactions from the transaction pool
// and mines a new block every interval seconds.
func (s *P2PServer) StartScheduler(interval float64) {
	ticker := time.NewTicker(time.Duration(interval * float64(time.Second)))
	go func() {
		for range ticker.C {
			if s.pbftEngine.IsLeader() {
				if len(s.bc.GetTransactionPool().GetTransactions()) > 0 {
					msg := message.Message{
						Type: message.SyncView,
						Data: mustMarshal(s.pbftEngine.View),
						ID:   uuid.New().String(),
						TTL:  10, // 기본 TTL
					}

					s.Broadcast(msg)

					log.Println("Scheduler triggered: Attempting to mine a new block")
					block := s.bc.MineBlock()
					if block.Index != 0 { // Check if a valid block was mined
						log.Printf("Scheduler successfully mined block: Index=%d, Hash=%s", block.Index, block.Hash)
						// 새 블록 브로드캐스트
						fmt.Println("블록 마이닝 전파!!!!")
						s.BroadcastNewBlock(block)

						s.pbftEngine.View += 1
						if len(s.peers)+1 < s.pbftEngine.View {
							s.pbftEngine.View = 1
						}
					}
				}

			}
		}
	}()
}

func NewP2PServer(bc *core.Blockchain, maxPeer int, listenAddr string) *P2PServer {
	nodeID, err := uuid.NewUUID()
	if err != nil {
		log.Fatalf("UUID 생성 실패: %v", err)
	}

	s := &P2PServer{
		bc:           bc,
		peers:        make(map[string]*net.TCPConn),
		reconnecting: make(map[string]bool),
		seenMessages: make(map[string]bool),
		nodeID:       nodeID.String(),
		nodeIDs:      []string{nodeID.String()},
		maxPeer:      maxPeer,
		gossipFanout: 3, // 기본 팬아웃 값
	}

	s.pbftEngine = pbft.NewPBFT(s.nodeID, s.BroadcastPBFTMessage, bc.AppendBlock)

	// TCP 리스너 시작
	addr, err := net.ResolveTCPAddr("tcp", listenAddr)
	if err != nil {
		log.Fatalf("TCP 주소 해석 실패: %v", err)
	}
	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		log.Fatalf("TCP 리스너 시작 실패: %v", err)
	}
	s.listener = listener

	s.StartScheduler(1)
	go s.acceptConnections()
	go s.sendHeartbeat() // 하트비트 주기적 전송

	return s
}

func (s *P2PServer) BroadcastPBFTMessage(msgType int, block core.Block) {
	payload := pbft.PBFTPayload{
		Block:    block,
		SenderID: s.pbftEngine.NodeID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Println("PBFT 메시지 직렬화 실패:", err)
		return
	}

	msg := message.Message{
		Type: msgType,
		Data: data,
		ID:   uuid.New().String(),
		TTL:  10, // 기본 TTL
	}

	s.Broadcast(msg)
}

func (s *P2PServer) acceptConnections() {
	for {
		conn, err := s.listener.AcceptTCP()
		if err != nil {
			log.Printf("TCP 연결 수락 실패: %v", err)
			continue
		}
		remoteAddr := util.NormalizeAddr(conn.RemoteAddr().String())
		s.peersMutex.Lock()
		if len(s.peers) >= s.maxPeer {
			s.peersMutex.Unlock()
			conn.Close()
			log.Printf("최대 피어 수 초과로 연결 거부: %s", remoteAddr)
			continue
		}
		s.peersMutex.Unlock()

		log.Printf("새 피어 연결됨: %s (임시 주소)", remoteAddr)
		go s.handleMessages(conn)
		s.sendMessage(conn, message.Message{Type: message.QUERY_LATEST, ID: uuid.New().String(), TTL: 10})
		s.sendMessage(conn, message.Message{Type: message.AddNodes, Data: mustMarshal(s.nodeIDs), ID: uuid.New().String(), TTL: 10})
		s.sendMessage(conn, message.Message{Type: message.AddPeers, Data: mustMarshal(s.GetPeers()), ID: uuid.New().String(), TTL: 10})
	}
}

func (s *P2PServer) AddNodes(ids []string) {
	if s.pbftEngine != nil {
		s.pbftEngine.UpdateNodeIDs(ids)
	}
}

func (s *P2PServer) onClose(conn *net.TCPConn, remoteAddr string, closeErr error) {
	normalizedAddr := util.NormalizeAddr(remoteAddr)
	s.peersMutex.Lock()
	delete(s.peers, normalizedAddr)
	s.peersMutex.Unlock()

	log.Printf("피어 연결 종료: %s, 에러: %v", normalizedAddr, closeErr)
	delete(s.peers, normalizedAddr)
	conn.Close()

	if closeErr != nil && !strings.Contains(closeErr.Error(), "use of closed network connection") {
		log.Printf("재접속 시도 시작: %s", normalizedAddr)
		s.tryReconnect(normalizedAddr)
	}
}

func (s *P2PServer) handleMessages(conn *net.TCPConn) {
	remoteAddr := conn.RemoteAddr().String()
	defer s.onClose(conn, remoteAddr, nil)

	for {
		lengthBytes := make([]byte, 4)
		_, err := io.ReadFull(conn, lengthBytes)
		if err != nil {
			s.onClose(conn, remoteAddr, err)
			return
		}
		length := binary.BigEndian.Uint32(lengthBytes)

		data := make([]byte, length)
		_, err = io.ReadFull(conn, data)
		if err != nil {
			s.onClose(conn, remoteAddr, err)
			return
		}

		var msg message.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("메시지 파싱 실패: %v", err)
			continue
		}

		s.processMessage(conn, msg, remoteAddr) // remoteAddr 추가
	}
}

func (s *P2PServer) processMessage(conn *net.TCPConn, msg message.Message, remoteAddr string) {
	s.seenMessagesMutex.Lock()
	if s.seenMessages[msg.ID] {
		s.seenMessagesMutex.Unlock()
		log.Printf("중복 메시지 무시: %s", msg.Data)
		return
	}
	s.seenMessages[msg.ID] = true
	s.seenMessagesMutex.Unlock()

	switch msg.Type {
	case message.QUERY_LATEST:
		latestBlock := s.bc.GetLastBlock()
		data, _ := json.Marshal([]core.Block{latestBlock})
		s.sendMessage(conn, message.Message{Type: message.RESPONSE_BLOCKCHAIN, Data: data, ID: uuid.New().String(), TTL: 10})

	case message.QUERY_ALL:
		blocks := s.bc.GetBlocks()
		data, _ := json.Marshal(blocks)
		s.sendMessage(conn, message.Message{Type: message.RESPONSE_BLOCKCHAIN, Data: data, ID: uuid.New().String(), TTL: 10})

	case message.RESPONSE_BLOCKCHAIN:
		s.handleBlockchainResponse(msg.Data)

	case message.MessageTypeTransaction:
		var tx core.Transaction
		if err := json.Unmarshal(msg.Data, &tx); err != nil {
			log.Println("트랜잭션 언마샬 실패:", err)
			return
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
			return
		}
		s.pbftEngine.OnPrePrepare(pbft)

	case message.MessageTypePBFTPrepare:
		var pbft pbft.PBFTPayload
		if err := json.Unmarshal(msg.Data, &pbft); err != nil {
			log.Println("Prepare 메시지 파싱 실패:", err)
			return
		}
		s.pbftEngine.OnPrepare(pbft)

	case message.MessageTypePBFTCommit:
		var pbft pbft.PBFTPayload
		if err := json.Unmarshal(msg.Data, &pbft); err != nil {
			log.Println("Commit 메시지 파싱 실패:", err)
			return
		}
		s.pbftEngine.OnCommit(pbft)

	case message.AddNodes:
		var nodeIDs []string
		if err := json.Unmarshal(msg.Data, &nodeIDs); err == nil {
			log.Println("AddNodes:", nodeIDs)
			s.AddNodes(nodeIDs)
		}

	case message.AddPeers:
		var peerAddresses []string
		if err := json.Unmarshal(msg.Data, &peerAddresses); err != nil {
			log.Println("피어 정보 역직렬화 실패:", err)
			return
		}
		log.Println("상대방 Peers:", peerAddresses)

		// 피어 주소로 peers 맵 갱신
		s.peersMutex.Lock()
		if _, ok := s.peers[remoteAddr]; ok {
			for _, addr := range peerAddresses {
				if addr == remoteAddr {
					// remoteAddr를 새로운 주소로 교체
					delete(s.peers, remoteAddr)
					s.peers[util.NormalizeAddr(addr)] = conn
					log.Printf("피어 주소 갱신: %s -> %s", remoteAddr, addr)
					break
				}
			}
		}
		s.peersMutex.Unlock()

		for _, addr := range peerAddresses {
			if err := s.ConnectToPeer(addr); err != nil {
				log.Printf("피어 연결 실패 (%s): %v", addr, err)
			} else {
				log.Printf("피어 연결 성공: %s", addr)
			}
		}
		// s.Broadcast(message.Message{Type: message.AddPeers, Data: mustMarshal(s.GetPeers()), ID: uuid.New().String(), TTL: 10})
	case message.SyncView:
		var view int
		if err := json.Unmarshal(msg.Data, &view); err != nil {
			log.Println("view 정보 역직렬화 실패:", err)
			return
		}
		log.Println("상대방 view:", view)
		s.SyncView(view)

	case message.HEARTBEAT:
		log.Printf("하트비트 수신: %s", msg.ID)
		return
	}

	if msg.TTL > 1 {
		s.Broadcast(msg)
	}
}

func (s *P2PServer) SyncView(view int) {
	s.pbftEngine.View = view
}

func (s *P2PServer) handleBlockchainResponse(data json.RawMessage) {
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
			data, _ := json.Marshal([]core.Block{latestReceivedBlock})
			s.Broadcast(message.Message{Type: message.RESPONSE_BLOCKCHAIN, Data: data, ID: uuid.New().String(), TTL: 10})
		} else if len(receivedBlocks) == 1 {
			s.Broadcast(message.Message{Type: message.QUERY_ALL, ID: uuid.New().String(), TTL: 10})
		} else {
			if s.bc.ReplaceChain(receivedBlocks) {
				log.Println("내 체인 전체 교체 성공")
				data, _ := json.Marshal(receivedBlocks)
				s.Broadcast(message.Message{Type: message.RESPONSE_BLOCKCHAIN, Data: data, ID: uuid.New().String(), TTL: 10})
			} else {
				log.Println("내 체인 전체 교체 실패: 유효하지 않은 체인")
			}
		}
	}
}

func (s *P2PServer) sendMessage(conn *net.TCPConn, msg message.Message) error {
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	if msg.TTL == 0 {
		msg.TTL = 10
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	length := uint32(len(data))
	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, length)

	_, err = conn.Write(lengthBytes)
	if err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

func (s *P2PServer) Broadcast(msg message.Message) {
	s.peersMutex.Lock()
	defer s.peersMutex.Unlock()

	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	if msg.TTL == 0 {
		msg.TTL = 10
	}
	if msg.TTL <= 1 {
		return
	}
	msg.TTL--

	peers := make([]*net.TCPConn, 0, len(s.peers))
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	rand.Shuffle(len(peers), func(i, j int) { peers[i], peers[j] = peers[j], peers[i] })

	selected := peers
	if len(peers) > s.gossipFanout {
		selected = peers[:s.gossipFanout]
	}

	for _, peer := range selected {
		if err := s.sendMessage(peer, msg); err != nil {
			log.Printf("Gossip 메시지 전송 실패: %v", err)
		}
	}
}

func (s *P2PServer) BroadcastNewBlock(block core.Block) {
	if s.pbftEngine.IsLeader() {
		s.pbftEngine.Broadcast(message.MessageTypePBFTPrePrepare, block)
	} else {
		log.Println("리더 노드가 아니므로 블록 생성 생략")
	}
}

func (s *P2PServer) ConnectToPeer(address string) error {
	s.peersMutex.Lock()
	normalizedAddr := util.NormalizeAddr(address)
	if _, ok := s.peers[normalizedAddr]; ok {
		s.peersMutex.Unlock()
		log.Printf("이미 연결된 피어: %s", normalizedAddr)
		return nil
	}
	if len(s.peers) >= s.maxPeer {
		s.peersMutex.Unlock()
		log.Printf("최대 피어 수 초과: %s", address)
		return fmt.Errorf("maximum peer limit reached")
	}
	s.reconnecting[normalizedAddr] = true
	s.peersMutex.Unlock()

	defer func() {
		s.peersMutex.Lock()
		delete(s.reconnecting, normalizedAddr)
		s.peersMutex.Unlock()
	}()

	tcpAddr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return fmt.Errorf("resolve TCP address failed: %v", err)
	}

	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return fmt.Errorf("TCP connect failed: %v", err)
	}

	// 서버 주소(normalizedAddr)를 peers 맵의 키로 사용
	s.peersMutex.Lock()
	s.peers[normalizedAddr] = conn
	s.peersMutex.Unlock()

	log.Printf("새 피어 연결 성공: %s", normalizedAddr)
	go s.handleMessages(conn)
	s.sendMessage(conn, message.Message{Type: message.QUERY_LATEST, ID: uuid.New().String(), TTL: 10})
	s.sendMessage(conn, message.Message{Type: message.AddNodes, Data: mustMarshal(s.nodeIDs), ID: uuid.New().String(), TTL: 10})
	s.sendMessage(conn, message.Message{Type: message.AddPeers, Data: mustMarshal(s.GetPeers()), ID: uuid.New().String(), TTL: 10})

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
		log.Printf("이미 재접속 시도 중, 재접속 스킵: %s", normalizedAddr)
		return
	}
	s.reconnecting[normalizedAddr] = true
	s.peersMutex.Unlock()

	defer func() {
		s.peersMutex.Lock()
		delete(s.reconnecting, normalizedAddr)
		delete(s.peers, address)
		s.peersMutex.Unlock()
		log.Printf("재접속 시도 종료: %s", normalizedAddr)
	}()

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
		err := s.ConnectToPeer(address)
		if err == nil {
			log.Printf("피어 재접속 성공: %s", normalizedAddr)
			return
		}
		backoff := time.Duration(2<<uint(attempt-1)) * time.Second
		log.Printf("재접속 실패 (%s), %d초 후 재시도: %v", normalizedAddr, backoff/time.Second, err)
		time.Sleep(backoff)
	}
	log.Printf("재접속 최대 시도 횟수 초과, 종료: %s", normalizedAddr)
	delete(s.peers, address)
}

func (s *P2PServer) BroadcastTransaction(tx core.Transaction) {
	msg := message.Message{
		Type: message.MessageTypeTransaction,
		Data: mustMarshal(tx),
		ID:   uuid.New().String(),
		TTL:  10,
	}
	s.Broadcast(msg)
}

func (s *P2PServer) sendHeartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		s.Broadcast(message.Message{Type: message.HEARTBEAT, ID: uuid.New().String(), TTL: 1})
	}
}

func mustMarshal(v interface{}) json.RawMessage {
	bytes, err := json.Marshal(v)
	if err != nil {
		log.Fatalf("Marshal 실패: %v", err)
	}
	return bytes
}
