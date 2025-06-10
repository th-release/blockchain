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
)

type Message struct {
	Type int             `json:"type"`
	Data json.RawMessage `json:"data"`
}
