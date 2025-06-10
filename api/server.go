package api

import (
	"cth-core.xyz/blockchain/core"
	"cth-core.xyz/blockchain/network"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type Server struct {
	App        *fiber.App
	Blockchain *core.Blockchain
	P2PServer  *network.P2PServer
}

func NewServer(p2pServer *network.P2PServer, bc *core.Blockchain) *Server {
	app := fiber.New()

	server := &Server{
		App:        app,
		Blockchain: bc,
		P2PServer:  p2pServer,
	}

	server.setupRoutes()
	return server
}

func (s *Server) setupRoutes() {
	s.App.Get("/blocks", s.getBlocks)
	s.App.Post("/transaction", s.handleCreateTransaction)
	s.App.Post("/mine", s.mineBlock)
	s.App.Get("/validate", s.validateChain)
	s.App.Get("/peers", s.handleGetPeers)
	s.App.Post("/addPeer", s.handleAddPeer)
	s.App.Get("/info", s.info)
}

func (s *Server) info(c *fiber.Ctx) error {
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"nodeId": s.P2PServer.Info().NodeID, "nodeIds": s.P2PServer.Info().NodeIDs, "view": s.P2PServer.Info().View, "isLeader": s.P2PServer.Info().IsLeader()})
}

func (s *Server) handleCreateTransaction(c *fiber.Ctx) error {
	var tx core.Transaction
	if err := c.BodyParser(&tx); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("잘못된 요청")
	}

	tx.ID = uuid.New().String()

	if s.Blockchain.AddTransaction(tx) {
		s.P2PServer.BroadcastTransaction(tx)
		return c.Status(fiber.StatusCreated).JSON(tx)
	}

	return c.Status(fiber.StatusConflict).SendString("중복 트랜잭션 또는 처리 불가")
}

func (s *Server) getBlocks(c *fiber.Ctx) error {
	blocks := s.Blockchain.GetBlocks()
	return c.Status(fiber.StatusOK).JSON(blocks)
}

type AddBlockRequest struct {
	Data string `json:"data"`
}

type AddTransactionRequest struct {
	Payload string `json:"payload"`
}

func (s *Server) mineBlock(c *fiber.Ctx) error {
	block := s.Blockchain.MineBlock()
	if block.Index == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "트랜잭션이 없습니다"})
	}

	// 새 블록 브로드캐스트
	s.P2PServer.BroadcastNewBlock(block)

	return c.Status(fiber.StatusCreated).JSON(block)
}

func (s *Server) validateChain(c *fiber.Ctx) error {
	valid := s.Blockchain.IsValid()
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"valid": valid,
	})
}

func (s *Server) handleGetPeers(c *fiber.Ctx) error {
	peers := s.P2PServer.GetPeers()
	return c.JSON(peers)
}

type AddPeerRequest struct {
	Address string `json:"address"`
}

func (s *Server) handleAddPeer(c *fiber.Ctx) error {
	var req AddPeerRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "잘못된 요청"})
	}

	if err := s.P2PServer.ConnectToPeer(req.Address); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"message": "피어 연결 성공"})
}
