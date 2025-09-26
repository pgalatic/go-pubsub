package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
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

// Client represents a PubSub client
type Client struct {
	nodeID       int
	conn         *websocket.Conn
	done         chan struct{}
	nodeAddr     string
	connected    bool
	messageCount int
}

func main() {
	// Display banner
	printBanner()

	if len(os.Args) != 2 {
		fmt.Println("Usage: go run client.go <node_id>")
		fmt.Println("Example: go run client.go 0")
		fmt.Println("Node IDs are typically 0, 1, 2, 3 (depending on how many nodes you started)")
		os.Exit(1)
	}

	nodeID, err := strconv.Atoi(os.Args[1])
	if err != nil || nodeID < 0 || nodeID > 3 {
		fmt.Println("Node ID must be between 0 and 3")
		os.Exit(1)
	}

	client := NewClient(nodeID)
	client.Start()
}

// printBanner displays the client banner
func printBanner() {
	fmt.Println("PubSub Client - Real-time Messaging")
}

// NewClient creates a new client instance
func NewClient(nodeID int) *Client {
	port := 8080 + nodeID
	nodeAddr := fmt.Sprintf("localhost:%d", port)

	return &Client{
		nodeID:    nodeID,
		nodeAddr:  nodeAddr,
		done:      make(chan struct{}),
		connected: false,
	}
}

// Start connects to the server and starts the client
func (c *Client) Start() {
	fmt.Printf("Connecting to Node %d at %s...\n", c.nodeID, c.nodeAddr)

	// Check if server is available first
	if !c.checkServerHealth() {
		fmt.Printf("Cannot connect to Node %d. Is the server running?\n", c.nodeID)
		fmt.Println("Start the server first with: go run main.go server.go <number_of_nodes>")
		os.Exit(1)
	}

	// Connect to WebSocket server
	err := c.connect()
	if err != nil {
		log.Fatalf("Failed to connect to node %d: %v", c.nodeID, err)
	}
	defer c.conn.Close()

	c.connected = true
	fmt.Printf("Connected to Node %d!\n", c.nodeID)
	c.showInstructions()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start goroutines
	go c.readMessages()
	go c.readInput()
	go func() {
		<-sigChan
		fmt.Println("\nDisconnecting...")
		close(c.done)
	}()

	// Wait for completion
	<-c.done
	c.connected = false
	fmt.Println("Client disconnected from Node", c.nodeID)
}

// checkServerHealth checks if the target server is running
func (c *Client) checkServerHealth() bool {
	client := &http.Client{Timeout: 3 * time.Second}
	url := fmt.Sprintf("http://%s/health", c.nodeAddr)

	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// connect establishes WebSocket connection to the server
func (c *Client) connect() error {
	u := url.URL{Scheme: "ws", Host: c.nodeAddr, Path: "/ws"}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	var err error
	c.conn, _, err = dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("dial error: %v", err)
	}

	// Set up connection parameters
	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(300 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	return nil
}

// showInstructions displays usage instructions
func (c *Client) showInstructions() {
	fmt.Println("\nInstructions:")
	fmt.Println("  • Type your messages and press Enter to broadcast")
	fmt.Println("  • Use /help to see available commands")
	fmt.Println("  • Use /quit or Ctrl+C to disconnect")
	fmt.Println("  • Messages will appear in real-time from all connected clients")
}

// readMessages continuously reads messages from the server
func (c *Client) readMessages() {
	// Start keepalive ticker
	ticker := time.NewTicker(54 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			// Send ping to keep connection alive
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("Ping failed: %v", err)
				close(c.done)
				return
			}
		default:
			var msg Message
			err := c.conn.ReadJSON(&msg)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("Connection error: %v", err)
				}
				close(c.done)
				return
			}

			c.messageCount++
			c.displayMessage(&msg)
		}
	}
}

// displayMessage formats and displays a received message
func (c *Client) displayMessage(msg *Message) {
	timestamp := msg.Timestamp.Format("15:04:05")

	fmt.Printf("[%s] Node %d: %s\n",
		timestamp, msg.NodeID, msg.Content)
}

// readInput continuously reads user input and sends messages
func (c *Client) readInput() {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		select {
		case <-c.done:
			return
		default:
			if !scanner.Scan() {
				if scanner.Err() != nil {
					log.Printf("Input error: %v", scanner.Err())
				}
				close(c.done)
				return
			}

			input := strings.TrimSpace(scanner.Text())
			if input == "" {
				continue
			}

			// Handle commands
			if strings.HasPrefix(input, "/") {
				c.handleCommand(input)
				continue
			}

			// Send message
			err := c.sendMessage(input)
			if err != nil {
				log.Printf("Error sending message: %v", err)
				close(c.done)
				return
			}
		}
	}
}

// sendMessage sends a message to the server
func (c *Client) sendMessage(content string) error {
	msg := Message{
		Content: content,
	}
	fmt.Printf("Sending message: %s\n", content)

	return c.conn.WriteJSON(msg)
}

// handleCommand processes client commands
func (c *Client) handleCommand(cmd string) {
	switch {
	case cmd == "/quit" || cmd == "/q" || cmd == "/exit":
		fmt.Println("Disconnecting...")
		// Send a close message to server before closing
		c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		close(c.done)

	case cmd == "/help" || cmd == "/h":
		c.showHelp()

	case cmd == "/status" || cmd == "/info":
		c.showStatus()

	case cmd == "/ping":
		err := c.sendMessage("PING from client")
		if err != nil {
			log.Printf("Error sending ping: %v", err)
		}

	case cmd == "/stats":
		c.showStats()

	case strings.HasPrefix(cmd, "/echo "):
		message := strings.TrimPrefix(cmd, "/echo ")
		err := c.sendMessage("Echo: " + message)
		if err != nil {
			log.Printf("Error sending echo: %v", err)
		}

	default:
		fmt.Printf("Unknown command: %s (type /help for available commands)\n", cmd)
	}
}

// showHelp displays available commands
func (c *Client) showHelp() {
	fmt.Println("Available Commands:")
	fmt.Println("  /help, /h         - Show this help message")
	fmt.Println("  /quit, /q, /exit  - Disconnect from server")
	fmt.Println("  /status, /info    - Show connection status")
	fmt.Println("  /ping             - Send a ping message")
	fmt.Println("  /stats            - Show message statistics")
	fmt.Println("  /echo <message>   - Echo a message with prefix")
	fmt.Println("\n Just type any message and press Enter to broadcast it!")
}

// showStatus displays connection status and server health
func (c *Client) showStatus() {
	fmt.Printf("Connection Status\n")
	fmt.Printf("  Connected to: Node %d\n", c.nodeID)
	fmt.Printf("  Address: %s\n", c.nodeAddr)
	fmt.Printf("  WebSocket URL: ws://%s/ws\n", c.nodeAddr)
	fmt.Printf("  Health Check: http://%s/health\n", c.nodeAddr)
	fmt.Printf("  Messages received: %d\n", c.messageCount)
	fmt.Printf("  Connected since: %s\n", time.Now().Format("15:04:05"))

	// Get server health info
	fmt.Printf("  Server Status: ")
	c.displayServerHealth()
}

// displayServerHealth shows detailed server health information
func (c *Client) displayServerHealth() {
	client := &http.Client{Timeout: 3 * time.Second}
	url := fmt.Sprintf("http://%s/health", c.nodeAddr)

	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("Cannot reach server (%v)\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Server issues (Status: %d)\n", resp.StatusCode)
		return
	}

	var health map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&health)
	if err != nil {
		fmt.Printf("Online (Cannot parse details)\n")
		return
	}

	fmt.Printf("Online\n")
	if clients, ok := health["clients"].(float64); ok {
		fmt.Printf("    Connected clients: %.0f\n", clients)
	}
}

// showStats displays message statistics
func (c *Client) showStats() {
	fmt.Println("Session Statistics")
	fmt.Printf("  Messages received: %d\n", c.messageCount)
	fmt.Printf("  Connected to: Node %d\n", c.nodeID)
	fmt.Printf("  Session time: Active\n")
	fmt.Printf("  Connection: %s\n", map[bool]string{true: "Active", false: "Disconnected"}[c.connected])
}
