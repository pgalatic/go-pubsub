package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Message represents a message in the PubSub system
type Message struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	NodeID    int       `json:"node_id"`
}

// Server represents a PubSub server node
type Server struct {
	nodeID        int
	port          int
	clients       map[*websocket.Conn]bool
	clientsMux    sync.RWMutex
	peers         []string            // Other server addresses
	messageBuffer map[string]*Message // For deduplication
	bufferMux     sync.RWMutex
	upgrader      websocket.Upgrader
	httpClient    *http.Client
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

// NewServer creates a new server instance
func NewServer(nodeID, port int, peers []string) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		nodeID:        nodeID,
		port:          port,
		clients:       make(map[*websocket.Conn]bool),
		peers:         peers,
		messageBuffer: make(map[string]*Message),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for simplicity
			},
		},
		httpClient: &http.Client{Timeout: 5 * time.Second},
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start starts the server
func (s *Server) Start() error {
	// Setup HTTP routes on a new ServeMux
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/api/message", s.handleMessage)
	mux.HandleFunc("/health", s.handleHealth)

	// Start background goroutines
	s.wg.Add(2)
	go s.sendPeriodicMessages()
	go s.cleanupOldMessages()

	log.Printf("Server %d starting on port %d", s.nodeID, s.port)

	// Start HTTP server
	server := &http.Server{
		Addr:    fmt.Sprintf("localhost:%d", s.port),
		Handler: mux,
	}

	go func() {
		<-s.ctx.Done()
		log.Printf("Server %d shutting down", s.nodeID)
		server.Shutdown(context.Background())
	}()

	return server.ListenAndServe()
}

// Stop gracefully stops the server
func (s *Server) Stop() {
	s.cancel()
	s.wg.Wait()
}

// handleWebSocket handles WebSocket connections
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Add client to the list
	s.clientsMux.Lock()
	s.clients[conn] = true
	clientCount := len(s.clients)
	s.clientsMux.Unlock()

	log.Printf("Client connected to node %d (#clients connected to this node: %d)", s.nodeID, clientCount)

	// Send welcome message
	welcomeMsg := Message{
		ID:        fmt.Sprintf("welcome-%d-%d", s.nodeID, time.Now().UnixNano()),
		Content:   fmt.Sprintf("Connected to node %d", s.nodeID),
		Timestamp: time.Now(),
		NodeID:    s.nodeID,
	}
	conn.WriteJSON(welcomeMsg)

	// Handle incoming messages from client
	for {
		var msg Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			// Don't log normal websocket closures as errors
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("Error reading message: %v", err)
			}
			break
		}

		// Add metadata
		msg.ID = fmt.Sprintf("client-%d-%d", s.nodeID, time.Now().UnixNano())
		msg.Timestamp = time.Now()
		msg.NodeID = s.nodeID

		// Broadcast message
		s.broadcastMessage(&msg)
	}

	// Remove client from the list
	s.clientsMux.Lock()
	delete(s.clients, conn)
	clientCount = len(s.clients)
	s.clientsMux.Unlock()

	log.Printf("Client disconnected from node %d (#clients connected to this node: %d)", s.nodeID, clientCount)
}

// handleMessage handles REST API messages from other nodes
func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg Message
	err := json.NewDecoder(r.Body).Decode(&msg)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Check for duplicate messages
	s.bufferMux.Lock()
	if _, exists := s.messageBuffer[msg.ID]; exists {
		s.bufferMux.Unlock()
		w.WriteHeader(http.StatusOK) // Already processed
		return
	}
	s.messageBuffer[msg.ID] = &msg
	s.bufferMux.Unlock()

	// Broadcast to local clients only (don't forward to peers to avoid loops)
	s.broadcastToLocalClients(&msg)

	w.WriteHeader(http.StatusOK)
}

// handleHealth provides a health check endpoint
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.clientsMux.RLock()
	clientCount := len(s.clients)
	s.clientsMux.RUnlock()

	response := map[string]interface{}{
		"node_id":   s.nodeID,
		"port":      s.port,
		"clients":   clientCount,
		"timestamp": time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// broadcastMessage broadcasts a message to all clients and peer servers
func (s *Server) broadcastMessage(msg *Message) {
	// Store message for deduplication
	s.bufferMux.Lock()
	s.messageBuffer[msg.ID] = msg
	s.bufferMux.Unlock()

	// Broadcast to local clients
	s.broadcastToLocalClients(msg)

	// Forward to peer servers
	s.forwardToPeers(msg)
}

// broadcastToLocalClients broadcasts message to local WebSocket clients
func (s *Server) broadcastToLocalClients(msg *Message) {
	s.clientsMux.RLock()
	clients := make([]*websocket.Conn, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.clientsMux.RUnlock()

	// Send to all clients
	for _, client := range clients {
		err := client.WriteJSON(msg)
		if err != nil {
			log.Printf("Error sending message to client: %v", err)
			// Remove disconnected client
			s.clientsMux.Lock()
			delete(s.clients, client)
			s.clientsMux.Unlock()
			client.Close()
		}
	}
}

// forwardToPeers forwards message to peer servers via REST API
func (s *Server) forwardToPeers(msg *Message) {
	msgJSON, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling message: %v", err)
		return
	}

	for _, peer := range s.peers {
		go func(peerAddr string) {
			url := fmt.Sprintf("http://%s/api/message", peerAddr)
			_, err := s.httpClient.Post(url, "application/json", bytes.NewBuffer(msgJSON))
			if err != nil {
				log.Printf("Error forwarding message to %s: %v", peerAddr, err)
			}
		}(peer)
	}
}

// sendPeriodicMessages sends random messages when no clients are connected
func (s *Server) sendPeriodicMessages() {
	defer s.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.clientsMux.RLock()
			hasClients := len(s.clients) > 0
			s.clientsMux.RUnlock()

			if !hasClients {
				// Generate random message
				randomNum := rand.Intn(10)
				msg := Message{
					ID:        fmt.Sprintf("auto-%d-%d", s.nodeID, time.Now().UnixNano()),
					Content:   fmt.Sprintf("Automated message from node %d: %d", s.nodeID, randomNum),
					Timestamp: time.Now(),
					NodeID:    s.nodeID,
				}

				log.Printf("Node %d sending automated message: %s", s.nodeID, msg.Content)
				s.broadcastMessage(&msg)
			}

			// Randomize next interval (5-10 seconds)
			nextInterval := time.Duration(5+rand.Intn(6)) * time.Second
			ticker.Reset(nextInterval)
		}
	}
}

// cleanupOldMessages removes old messages from buffer to prevent memory leaks
func (s *Server) cleanupOldMessages() {
	defer s.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.bufferMux.Lock()
			cutoff := time.Now().Add(-5 * time.Minute)
			for id, msg := range s.messageBuffer {
				if msg.Timestamp.Before(cutoff) {
					delete(s.messageBuffer, id)
				}
			}
			s.bufferMux.Unlock()
		}
	}
}

// GetClientCount returns the number of connected clients
func (s *Server) GetClientCount() int {
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()
	return len(s.clients)
}
