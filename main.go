package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"cth-core.xyz/blockchain/api"
	"cth-core.xyz/blockchain/core"
	"cth-core.xyz/blockchain/network"
)

func getLocalIP() string {
	// 네트워크 인터페이스에서 로컬 IP를 가져옴
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1" // 에러 발생 시 기본값
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1" // 기본적으로 localhost 반환
}

func main() {
	// CLI = START
	maxPeer := flag.Int("max-peer", 5, "최대 피어 연결 갯수")
	url := flag.String("url", "", "도메인 또는 IP 주소 (기본값: 로컬 IP)")
	apiPort := flag.Int("api-port", 8888, "API 서버 포트")
	p2pPort := flag.Int("p2p-port", 9999, "P2P 서버 포트")
	peers := flag.String("peers", "", "쉼표(,)로 구분된 초기 피어 주소 목록 (예: localhost:9999,localhost:7777)")

	flag.Parse()

	host := *url

	if host == "" {
		host = fmt.Sprintf("%s:%d", getLocalIP(), *p2pPort)
	}

	fmt.Printf("API 포트: %d | P2P 포트: %d | 호스트: %s\n", *apiPort, *p2pPort, host)
	bc := core.NewBlockchain()
	p2pserver := network.NewP2PServer(bc, *maxPeer)

	// 초기 피어 연결 시도
	if *peers != "" {
		peerList := strings.Split(*peers, ",")

		for _, addr := range peerList {
			go p2pserver.ConnectToPeer(addr)
		}
	}

	server := api.NewServer(p2pserver, bc)

	go func() {
		http.HandleFunc("/ws", p2pserver.HandleConnections)
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *p2pPort), nil))
	}()

	server.App.Listen(fmt.Sprintf(":%d", *apiPort))
}
