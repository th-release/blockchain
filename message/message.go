package message

import "encoding/json"

const (
	QUERY_LATEST              = 0
	QUERY_ALL                 = 1
	RESPONSE_BLOCKCHAIN       = 2
	MessageTypeTransaction    = 3
	MessageTypePBFTPrePrepare = 4
	MessageTypePBFTPrepare    = 5
	MessageTypePBFTCommit     = 6
	AddNodeId                 = 100
	AddPeers                  = 101
	HEARTBEAT                 = 1000 // 하트비트 메시지
)

// type Message struct {
// 	Type int             `json:"type"`
// 	Data json.RawMessage `json:"data"`
// }

type Message struct {
	Type int             `json:"type"`
	Data json.RawMessage `json:"data"`
	ID   string          `json:"id"`  // 메시지 고유 ID (Gossip 중복 방지용)
	TTL  int             `json:"ttl"` // 메시지 전파 제한 (Time-To-Live)
}
