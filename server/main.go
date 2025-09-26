package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Controller manages multiple server instances
type Controller struct {
	numNodes int
	servers  []*ServerProcess
	wg       sync.WaitGroup
}

// ServerProcess represents a running server process
type ServerProcess struct {
	nodeID int
	port   int
	cancel context.CancelFunc
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run main.go <number_of_nodes>")
		fmt.Println("Example: go run main.go 3")
		os.Exit(1)
	}

	numNodes, err := strconv.Atoi(os.Args[1])
	if err != nil || numNodes < 1 || numNodes > 4 {
		fmt.Println("Number of nodes must be between 1 and 4")
		os.Exit(1)
	}

	controller := &Controller{
		numNodes: numNodes,
		servers:  make([]*ServerProcess, 0, numNodes),
	}
	controller.Start()
}

// Start initializes and starts all servers
func (c *Controller) Start() {
	fmt.Printf("Starting PubSub system with %d nodes...\n", c.numNodes)

	// Check for existing processes on ports
	c.checkPorts()

	// Start servers
	for i := 0; i < c.numNodes; i++ {
		nodeID := i
		port := 8080 + i

		server := c.startServer(nodeID, port)
		c.servers = append(c.servers, server)

		// Small delay between server starts
		time.Sleep(200 * time.Millisecond)
	}

	// Wait for servers to be ready
	c.waitForServers()
	c.printStatus()

	// Handle shutdown signals
	c.handleShutdown()
}

// startServer starts a single server process
func (c *Controller) startServer(nodeID, port int) *ServerProcess {
	ctx, cancel := context.WithCancel(context.Background())

	// Create peer list for this server
	peers := make([]string, 0, c.numNodes-1)
	for i := 0; i < c.numNodes; i++ {
		if i != nodeID {
			peerPort := 8080 + i
			peers = append(peers, fmt.Sprintf("localhost:%d", peerPort))
		}
	}

	server := &ServerProcess{
		nodeID: nodeID,
		port:   port,
		cancel: cancel,
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.runServerProcess(ctx, server, peers)
	}()

	return server
}

// runServerProcess runs the actual server logic in a goroutine
func (c *Controller) runServerProcess(ctx context.Context, sp *ServerProcess, peers []string) {
	// Create server instance
	server := NewServer(sp.nodeID, sp.port, peers)

	// Start server in a goroutine
	serverDone := make(chan error, 1)
	go func() {
		err := server.Start()
		serverDone <- err
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		server.Stop()
	case err := <-serverDone:
		if err != nil && err.Error() != "http: Server closed" {
			log.Printf("Server %d error: %v", sp.nodeID, err)
		}
	}
}

// checkPorts verifies that required ports are available
func (c *Controller) checkPorts() {
	for i := 0; i < c.numNodes; i++ {
		port := 8080 + i
		if c.isPortInUse(port) {
			fmt.Printf("Error: Port %d is already in use.\n", port)
			fmt.Printf("Please ensure no other processes are using ports 8080-%d\n", 8080+c.numNodes-1)
			fmt.Println("\nTo check what's using these ports:")
			if runtime.GOOS == "windows" {
				fmt.Printf("  netstat -an | findstr :%d\n", port)
			} else {
				fmt.Printf("  lsof -i :%d\n", port)
				fmt.Println("  sudo kill -9 <PID>")
			}
			os.Exit(1)
		}
	}
}

// isPortInUse checks if a port is currently in use
func (c *Controller) isPortInUse(port int) bool {
	conn, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	conn.Close()
	return false
}

// waitForServers waits for all servers to be ready
func (c *Controller) waitForServers() {
	fmt.Print("Waiting for servers to start")
	maxWait := 30 // seconds

	for range maxWait {
		fmt.Print(".")
		allReady := true

		for i := 0; i < c.numNodes; i++ {
			port := 8080 + i
			if !c.isServerReady(port) {
				allReady = false
				break
			}
		}

		if allReady {
			fmt.Println(" ✓")
			return
		}

		time.Sleep(1 * time.Second)
	}

	fmt.Println(" ✗")
	fmt.Println("Warning: Some servers may not be ready")
}

// isServerReady checks if a server is ready by testing its health endpoint
func (c *Controller) isServerReady(port int) bool {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/health", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// handleShutdown handles graceful shutdown on SIGINT/SIGTERM
func (c *Controller) handleShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("\nPress Ctrl+C to shutdown all servers...")
	<-sigChan

	fmt.Println("\nShutting down servers...")

	// Cancel all server contexts
	for i, server := range c.servers {
		if server != nil {
			log.Printf("Stopping server %d", i)
			server.cancel()
		}
	}

	// Wait for all servers to stop with timeout, but handle second SIGINT
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Give servers a moment to finish logging
		time.Sleep(500 * time.Millisecond)
		fmt.Println("All servers stopped successfully")
	case <-sigChan:
		fmt.Println("Second SIGINT received, forcing shutdown")
		return
	case <-time.After(10 * time.Second):
		fmt.Println("Timeout waiting for servers to stop")
	}
}

// printStatus prints the current status of all servers
func (c *Controller) printStatus() {
	fmt.Printf("PubSub Cluster Started Successfully\n")
	fmt.Printf("Number of nodes: %d\n", c.numNodes)
	fmt.Printf("Ports: 8080-%d\n", 8080+c.numNodes-1)
	fmt.Printf("\nWebSocket endpoints:\n")

	for i := 0; i < c.numNodes; i++ {
		port := 8080 + i
		fmt.Printf("  Node %d: ws://localhost:%d/ws\n", i, port)
	}

	fmt.Printf("\nHealth check endpoints:\n")
	for i := 0; i < c.numNodes; i++ {
		port := 8080 + i
		fmt.Printf("  Node %d: http://localhost:%d/health\n", i, port)
	}

	fmt.Printf("\nTo connect a client:\n")
	fmt.Printf("  go run client.go <node_id>\n")
	fmt.Printf("  Example: go run client.go 0\n")

	fmt.Printf("\nTest the system:\n")
	fmt.Printf("  curl http://localhost:8080/health\n")
	fmt.Printf("  # Open multiple terminals and run clients\n")

	// Show real-time status
	c.showRealTimeStatus()
}

// showRealTimeStatus displays ongoing status information
func (c *Controller) showRealTimeStatus() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Show initial status
	c.displayCurrentStatus()

	// Update status periodically
	go func() {
		for range ticker.C {
			c.displayCurrentStatus()
		}
	}()
}

// displayCurrentStatus shows current status of all nodes
func (c *Controller) displayCurrentStatus() {
	fmt.Printf("\nCluster Status [%s]:\n", time.Now().Format("15:04:05"))

	client := &http.Client{Timeout: 2 * time.Second}

	for i := 0; i < c.numNodes; i++ {
		port := 8080 + i
		url := fmt.Sprintf("http://localhost:%d/health", port)

		resp, err := client.Get(url)
		if err != nil {
			fmt.Printf("  Node %d: Offline (%v)\n", i, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			fmt.Printf("  Node %d: Online (Port %d)\n", i, port)
		} else {
			fmt.Printf("  Node %d:  Issues (Status %d)\n", i, resp.StatusCode)
		}
	}
}
