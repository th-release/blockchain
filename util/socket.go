package util

import (
	"time"

	"github.com/gorilla/websocket"
)

func IsConnectionAlive(conn *websocket.Conn) bool {
	// 1초 이내 응답 없으면 타임아웃
	deadline := time.Now().Add(1 * time.Second)
	conn.SetWriteDeadline(deadline)

	// ping 메시지를 보내서 연결 상태 확인
	err := conn.WriteMessage(websocket.PingMessage, []byte{})
	if err != nil {
		return false
	}
	return true
}
