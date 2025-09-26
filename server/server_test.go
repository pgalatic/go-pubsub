package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestServer_NewServer verifies that the NewServer constructor correctly initializes
// a server instance with the provided nodeID, port, and peer list. This ensures
// basic server configuration is set up properly before starting any services.
func TestServer_NewServer(t *testing.T) {
	peers := []string{"localhost:8081", "localhost:8082"}
	server := NewServer(0, 8080, peers)

	if server.nodeID != 0 {
		t.Errorf("Expected nodeID 0, got %d", server.nodeID)
	}
	if server.port != 8080 {
		t.Errorf("Expected port 8080, got %d", server.port)
	}
	if len(server.peers) != 2 {
		t.Errorf("Expected 2 peers, got %d", len(server.peers))
	}
}

// TestServer_HealthEndpoint verifies that the /health endpoint returns a valid HTTP 200
// response with correctly formatted JSON containing node_id and port information.
// This test ensures the health check functionality works for monitoring server status.
func TestServer_HealthEndpoint(t *testing.T) {
	server := NewServer(0, 8080, []string{})

	req, err := http.NewRequest("GET", "/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(server.handleHealth)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	var response map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	if err != nil {
		t.Errorf("Error parsing JSON response: %v", err)
	}

	if response["node_id"] != float64(0) {
		t.Errorf("Expected node_id 0, got %v", response["node_id"])
	}
	if response["port"] != float64(8080) {
		t.Errorf("Expected port 8080, got %v", response["port"])
	}
}

// TestServer_MessageEndpoint tests the /api/message POST endpoint that handles inter-server
// communication. It verifies that valid JSON messages are accepted, return HTTP 200,
// and are properly stored in the message buffer for deduplication tracking.
func TestServer_MessageEndpoint(t *testing.T) {
	server := NewServer(0, 8080, []string{})

	msg := Message{
		ID:        "test-msg-1",
		Content:   "Test message",
		Timestamp: time.Now(),
		NodeID:    1,
	}

	msgJSON, _ := json.Marshal(msg)
	req, err := http.NewRequest("POST", "/api/message", bytes.NewBuffer(msgJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(server.handleMessage)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	// Verify message was stored for deduplication
	server.bufferMux.RLock()
	_, exists := server.messageBuffer[msg.ID]
	server.bufferMux.RUnlock()

	if !exists {
		t.Error("Message was not stored in buffer for deduplication")
	}
}

// TestServer_MessageDeduplication verifies the "send at most once" semantics by ensuring
// that when the same message (same ID) is sent twice to the /api/message endpoint,
// only one copy is stored in the deduplication buffer. This prevents message loops
// and duplicate delivery in the distributed system.
func TestServer_MessageDeduplication(t *testing.T) {
	server := NewServer(0, 8080, []string{})

	msg := Message{
		ID:        "duplicate-test",
		Content:   "Duplicate message",
		Timestamp: time.Now(),
		NodeID:    1,
	}

	msgJSON, _ := json.Marshal(msg)

	// Send message first time
	req1, _ := http.NewRequest("POST", "/api/message", bytes.NewBuffer(msgJSON))
	req1.Header.Set("Content-Type", "application/json")
	rr1 := httptest.NewRecorder()
	handler := http.HandlerFunc(server.handleMessage)
	handler.ServeHTTP(rr1, req1)

	// Send same message again
	req2, _ := http.NewRequest("POST", "/api/message", bytes.NewBuffer(msgJSON))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	// Both should return OK but second should be ignored
	if rr1.Code != http.StatusOK || rr2.Code != http.StatusOK {
		t.Error("Both requests should return OK")
	}

	// Verify only one copy in buffer
	server.bufferMux.RLock()
	count := 0
	for id := range server.messageBuffer {
		if id == msg.ID {
			count++
		}
	}
	server.bufferMux.RUnlock()

	if count != 1 {
		t.Errorf("Expected 1 copy of message in buffer, got %d", count)
	}
}

// TestServer_WebSocketConnection tests basic WebSocket connectivity by establishing
// a connection, verifying the client count increases, sending a test message, and
// receiving the welcome message. This ensures the real-time messaging foundation works.
func TestServer_WebSocketConnection(t *testing.T) {
	server := NewServer(0, 8080, []string{})

	// Start test server
	testServer := httptest.NewServer(http.HandlerFunc(server.handleWebSocket))
	defer testServer.Close()

	// Convert HTTP URL to WebSocket URL
	u, _ := url.Parse(testServer.URL)
	u.Scheme = "ws"
	u.Path = "/ws"

	// Connect via WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket: %v", err)
	}
	defer conn.Close()

	// Wait a moment for connection to be registered
	time.Sleep(100 * time.Millisecond)

	// Check client count
	clientCount := server.GetClientCount()
	if clientCount != 1 {
		t.Errorf("Expected 1 client, got %d", clientCount)
	}

	// Send a test message
	testMsg := Message{Content: "Test client message"}
	err = conn.WriteJSON(testMsg)
	if err != nil {
		t.Errorf("Failed to send message: %v", err)
	}

	// Read welcome message
	var welcomeMsg Message
	err = conn.ReadJSON(&welcomeMsg)
	if err != nil {
		t.Errorf("Failed to read welcome message: %v", err)
	}

	if !strings.Contains(welcomeMsg.Content, "Connected to node") {
		t.Errorf("Unexpected welcome message: %s", welcomeMsg.Content)
	}
}

// TestServer_ClientCount verifies that the server correctly tracks the number of
// connected WebSocket clients. It tests connecting multiple clients and confirms
// the count reflects the actual connections. Note that disconnect cleanup is
// asynchronous, so immediate count decreases after close aren't guaranteed.
func TestServer_ClientCount(t *testing.T) {
	server := NewServer(0, 8080, []string{})

	// Initially should have 0 clients
	if server.GetClientCount() != 0 {
		t.Error("Expected 0 clients initially")
	}

	// Start test server
	testServer := httptest.NewServer(http.HandlerFunc(server.handleWebSocket))
	defer testServer.Close()

	u, _ := url.Parse(testServer.URL)
	u.Scheme = "ws"
	u.Path = "/ws"

	// Connect multiple clients
	conn1, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()

	conn2, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()

	// Wait for connections to be registered
	time.Sleep(100 * time.Millisecond)

	if server.GetClientCount() != 2 {
		t.Errorf("Expected 2 clients, got %d", server.GetClientCount())
	}
}

// TestServer_InvalidJSONMessage ensures the /api/message endpoint properly validates
// incoming JSON and returns HTTP 400 Bad Request for malformed data.
func TestServer_InvalidJSONMessage(t *testing.T) {
	server := NewServer(0, 8080, []string{})

	req, err := http.NewRequest("POST", "/api/message", bytes.NewBufferString("invalid json"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(server.handleMessage)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Handler should return bad request for invalid JSON: got %v want %v",
			status, http.StatusBadRequest)
	}
}

// TestServer_MethodNotAllowed verifies that the /api/message endpoint only accepts
// POST requests and returns HTTP 405 Method Not Allowed for other HTTP methods.
func TestServer_MethodNotAllowed(t *testing.T) {
	server := NewServer(0, 8080, []string{})

	req, err := http.NewRequest("GET", "/api/message", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(server.handleMessage)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusMethodNotAllowed {
		t.Errorf("Handler should return method not allowed for GET: got %v want %v",
			status, http.StatusMethodNotAllowed)
	}
}

// BenchmarkServer_MessageProcessing measures the performance of processing messages
// through the /api/message endpoint. This helps identify performance bottlenecks.
func BenchmarkServer_MessageProcessing(b *testing.B) {
	server := NewServer(0, 8080, []string{})

	msg := Message{
		ID:        "bench-test",
		Content:   "Benchmark message",
		Timestamp: time.Now(),
		NodeID:    1,
	}
	var msgJSON []byte

	for i := 0; b.Loop(); i++ {
		msg.ID = fmt.Sprintf("bench-test-%d", i)
		msgJSON, _ = json.Marshal(msg)

		req, _ := http.NewRequest("POST", "/api/message", bytes.NewBuffer(msgJSON))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(server.handleMessage)
		handler.ServeHTTP(rr, req)
	}
}

// TestCrossServerDelivery tests the distributed aspects of the system by starting
// two separate server instances and verifying they can communicate end to end.
func TestCrossServerDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Start server1 (8080) and server2 (8081)
	server1 := NewServer(0, 8080, []string{"localhost:8081"})
	server2 := NewServer(1, 8081, []string{"localhost:8080"})

	go func() { server1.Start() }()
	go func() { server2.Start() }()

	// Wait for servers to start
	waitForServer := func(url string) error {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := http.Get(url)
			if err == nil {
				resp.Body.Close()
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		return fmt.Errorf("server at %s not responding in time", url)
	}

	// Wait for servers
	waitForServer("http://localhost:8080/health")
	waitForServer("http://localhost:8081/health")

	// Connect WS client to server1
	ws1, _, err := websocket.DefaultDialer.Dial("ws://localhost:8080/ws", nil)
	if err != nil {
		t.Fatalf("Failed to connect to server1 WebSocket: %v", err)
	}
	defer ws1.Close()
	// discard server1's welcome
	_, _, _ = ws1.ReadMessage()

	// Connect WS client to server2
	ws2, _, err := websocket.DefaultDialer.Dial("ws://localhost:8081/ws", nil)
	if err != nil {
		t.Fatalf("Failed to connect to server2 WebSocket: %v", err)
	}
	defer ws2.Close()
	// discard server2's welcome
	_, _, _ = ws2.ReadMessage()

	// Send message via server1's WS
	msg := Message{
		ID:        "integration-test",
		Content:   "Cross-server test message",
		Timestamp: time.Now(),
		NodeID:    0,
	}
	if err := ws1.WriteJSON(msg); err != nil {
		t.Fatalf("Failed to write message to server1: %v", err)
	}

	// Now expect it on server2
	ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
	var received Message
	if err := ws2.ReadJSON(&received); err != nil {
		t.Fatalf("Failed to read message from server2: %v", err)
	}

	if received.Content != msg.Content {
		t.Errorf("Expected content %q, got %q", msg.Content, received.Content)
	}
}
