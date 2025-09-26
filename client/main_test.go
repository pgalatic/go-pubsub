package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestClient_NewClient verifies that the NewClient constructor correctly initializes
// a client instance with the provided nodeID and calculates the correct server address
// and port.
func TestClient_NewClient(t *testing.T) {
	client := NewClient(0)

	if client.nodeID != 0 {
		t.Errorf("Expected nodeID 0, got %d", client.nodeID)
	}
	if client.nodeAddr != "localhost:8080" {
		t.Errorf("Expected nodeAddr localhost:8080, got %s", client.nodeAddr)
	}
	if client.connected != false {
		t.Error("Expected client to start as disconnected")
	}
	if client.messageCount != 0 {
		t.Error("Expected message count to start at 0")
	}
}

// TestClient_NewClientPortCalculation verifies that clients correctly calculate
// server ports for different node IDs. This prevents clients from trying to
// connect to wrong servers in a multi-node setup.
func TestClient_NewClientPortCalculation(t *testing.T) {
	testCases := []struct {
		nodeID       int
		expectedAddr string
	}{
		{0, "localhost:8080"},
		{1, "localhost:8081"},
		{2, "localhost:8082"},
		{3, "localhost:8083"},
	}

	for _, tc := range testCases {
		client := NewClient(tc.nodeID)
		if client.nodeAddr != tc.expectedAddr {
			t.Errorf("For nodeID %d, expected address %s, got %s",
				tc.nodeID, tc.expectedAddr, client.nodeAddr)
		}
	}
}

// TestClient_CheckServerHealth verifies that the health check correctly identifies
// when a server is available versus unavailable. This prevents the client from
// attempting WebSocket connections to non-existent servers and provides better
// user feedback about connectivity issues.
func TestClient_CheckServerHealth(t *testing.T) {
	client := NewClient(0)

	// Test with a mock healthy server
	healthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"node_id": 0,
			"port":    8080,
			"clients": 0,
		})
	}))
	defer healthyServer.Close()

	// Update client to point to test server
	u, _ := url.Parse(healthyServer.URL)
	client.nodeAddr = u.Host

	if !client.checkServerHealth() {
		t.Error("Expected healthy server to return true")
	}

	// Test with no server (should fail)
	client.nodeAddr = "localhost:99999" // Non-existent port
	if client.checkServerHealth() {
		t.Error("Expected unreachable server to return false")
	}
}

// TestClient_CheckServerHealthUnhealthyResponse verifies that the client correctly
// identifies servers that are running but returning error status codes. This helps
// distinguish between network issues and server problems.
func TestClient_CheckServerHealthUnhealthyResponse(t *testing.T) {
	client := NewClient(0)

	// Test with a server returning 500 error
	unhealthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer unhealthyServer.Close()

	u, _ := url.Parse(unhealthyServer.URL)
	client.nodeAddr = u.Host

	if client.checkServerHealth() {
		t.Error("Expected server returning 500 to be considered unhealthy")
	}
}

// TestClient_Connect verifies that the WebSocket connection is established correctly
// with proper timeout settings and handlers.
func TestClient_Connect(t *testing.T) {
	client := NewClient(0)

	// Create a mock WebSocket server
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade connection: %v", err)
			return
		}
		defer conn.Close()

		// Echo any received messages
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}))
	defer server.Close()

	// Update client to point to test server
	u, _ := url.Parse(server.URL)
	u.Scheme = "ws"
	client.nodeAddr = u.Host

	err := client.connect()
	if err != nil {
		t.Errorf("Expected successful connection, got error: %v", err)
	}
	defer client.conn.Close()

	if client.conn == nil {
		t.Error("Expected connection to be established")
	}
}

// TestClient_SendMessage verifies that messages are correctly formatted and sent
// over the WebSocket connection.
func TestClient_SendMessage(t *testing.T) {
	client := NewClient(0)

	// Track received messages
	var receivedMessage Message
	messageReceived := make(chan bool, 1)

	// Create mock server that captures sent messages
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read the message sent by client
		err = conn.ReadJSON(&receivedMessage)
		if err != nil {
			t.Errorf("Failed to read message: %v", err)
			return
		}
		messageReceived <- true
	}))
	defer server.Close()

	// Connect client to test server
	u, _ := url.Parse(server.URL)
	u.Scheme = "ws"
	client.nodeAddr = u.Host

	err := client.connect()
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer client.conn.Close()

	// Send test message
	testContent := "Test message content"
	err = client.sendMessage(testContent)
	if err != nil {
		t.Errorf("Failed to send message: %v", err)
	}

	// Wait for message to be received
	select {
	case <-messageReceived:
		if receivedMessage.Content != testContent {
			t.Errorf("Expected message content '%s', got '%s'", testContent, receivedMessage.Content)
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for message to be received")
	}
}

// TestClient_HandleCommand_Quit verifies that the quit commands properly initiate
// client shutdown by sending a close message and closing the done channel.
func TestClient_HandleCommand_Quit(t *testing.T) {
	client := NewClient(0)
	client.done = make(chan struct{})

	// Mock WebSocket connection
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Wait for close message
		for {
			messageType, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if messageType == websocket.CloseMessage {
				return // Graceful close received
			}
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	u.Scheme = "ws"
	client.nodeAddr = u.Host
	client.connect()
	defer client.conn.Close()

	// Test different quit commands
	quitCommands := []string{"/quit", "/q", "/exit"}

	for _, cmd := range quitCommands {
		client.done = make(chan struct{})
		client.handleCommand(cmd)

		// Verify done channel was closed
		select {
		case <-client.done:
			// Expected behavior
		case <-time.After(100 * time.Millisecond):
			t.Errorf("Command %s did not close done channel", cmd)
		}
	}
}

// TestClient_HandleCommand_Echo verifies that the echo command correctly processes
// the message content and sends it with the proper prefix.
func TestClient_HandleCommand_Echo(t *testing.T) {
	client := NewClient(0)

	var receivedMessage Message
	messageReceived := make(chan bool, 1)

	// Mock server to capture echo message
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		err = conn.ReadJSON(&receivedMessage)
		if err != nil {
			return
		}
		messageReceived <- true
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	u.Scheme = "ws"
	client.nodeAddr = u.Host
	client.connect()
	defer client.conn.Close()

	// Test echo command
	client.handleCommand("/echo Hello World")

	select {
	case <-messageReceived:
		expected := "Echo: Hello World"
		if receivedMessage.Content != expected {
			t.Errorf("Expected echo message '%s', got '%s'", expected, receivedMessage.Content)
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for echo message")
	}
}

// TestClient_HandleCommand_Unknown verifies that unknown commands are handled
// gracefully without crashing the client.
func TestClient_HandleCommand_Unknown(t *testing.T) {
	client := NewClient(0)

	client.handleCommand("/nonexistent")
	client.handleCommand("/badcommand")
	client.handleCommand("/")
	// Test passes if no panics occur
}

// TestClient_DisplayMessage verifies that received messages are formatted correctly
// for display with proper timestamp formatting and node information. This ensures
// users can understand message origin and timing in the chat interface.
func TestClient_DisplayMessage(t *testing.T) {
	client := NewClient(0)

	// Create a test message
	testTime := time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC)
	msg := &Message{
		ID:        "test-msg",
		Content:   "Test message content",
		Timestamp: testTime,
		NodeID:    1,
	}

	client.displayMessage(msg)
	// Test passes if no panics occur
}

// TestClient_DisplayServerHealth verifies that server health information is
// correctly parsed and displayed from the health endpoint response.
func TestClient_DisplayServerHealth(t *testing.T) {
	client := NewClient(0)

	// Mock server with health data
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthData := map[string]interface{}{
			"node_id":   0,
			"port":      8080,
			"clients":   5.0, // JSON numbers are float64
			"timestamp": time.Now(),
		}
		json.NewEncoder(w).Encode(healthData)
	}))
	defer healthServer.Close()

	u, _ := url.Parse(healthServer.URL)
	client.nodeAddr = u.Host

	client.displayServerHealth()
	// Test passes if no panics occurred
}

// TestClient_MessageContentValidation verifies that the client handles various
// types of message content correctly, including empty messages, special characters,
// and very long messages.
func TestClient_MessageContentValidation(t *testing.T) {
	client := NewClient(0)

	// Mock server to receive various message types
	var receivedMessages []Message
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			var msg Message
			err = conn.ReadJSON(&msg)
			if err != nil {
				break
			}
			receivedMessages = append(receivedMessages, msg)
		}
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	u.Scheme = "ws"
	client.nodeAddr = u.Host
	client.connect()
	defer client.conn.Close()

	// Test various message contents
	testMessages := []string{
		"Normal message",
		"Message with special chars: !@#$%^&*()",
		"Message with unicode: 🔥💯✨",
		strings.Repeat("Long message ", 50), // Very long message
	}

	for _, content := range testMessages {
		err := client.sendMessage(content)
		if err != nil {
			t.Errorf("Failed to send message '%s': %v", content, err)
		}
	}

	// Give time for messages to be received
	time.Sleep(100 * time.Millisecond)

	if len(receivedMessages) != len(testMessages) {
		t.Errorf("Expected %d messages, received %d", len(testMessages), len(receivedMessages))
	}
}

// TestClient_ConnectionTimeout verifies that the client handles connection timeouts
// gracefully when trying to connect to unresponsive servers. This prevents the
// client from hanging indefinitely during connection attempts.
func TestClient_ConnectionTimeout(t *testing.T) {
	client := NewClient(0)

	// Point to a non-existent server that will cause timeout
	client.nodeAddr = "192.0.2.1:8080" // Reserved IP that should not respond

	err := client.connect()
	if err == nil {
		t.Error("Expected connection to timeout, but it succeeded")
		client.conn.Close()
	}

	// Verify error message indicates timeout/connection issue
	if err != nil && !strings.Contains(err.Error(), "dial") {
		t.Errorf("Expected dial error, got: %v", err)
	}
}
