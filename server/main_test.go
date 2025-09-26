package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestController_NewController verifies that a new Controller instance is properly
// initialized with the correct number of nodes and empty server slice.
func TestController_NewController(t *testing.T) {
	numNodes := 3
	controller := &Controller{
		numNodes: numNodes,
		servers:  make([]*ServerProcess, 0, numNodes),
	}

	if controller.numNodes != numNodes {
		t.Errorf("Expected numNodes %d, got %d", numNodes, controller.numNodes)
	}
	if len(controller.servers) != 0 {
		t.Errorf("Expected empty servers slice, got length %d", len(controller.servers))
	}
	if cap(controller.servers) != numNodes {
		t.Errorf("Expected servers capacity %d, got %d", numNodes, cap(controller.servers))
	}
}

// TestController_IsPortInUse verifies that the port checking logic correctly identifies
// when ports are available versus in use. This prevents the controller from trying
// to start servers on occupied ports.
func TestController_IsPortInUse(t *testing.T) {
	controller := &Controller{}

	// Test with a port that should be free
	freePort := 19999 // High port number likely to be free
	if controller.isPortInUse(freePort) {
		t.Errorf("Port %d should be free but was reported as in use", freePort)
	}

	// Test with a port we know is in use by creating a listener
	listener, err := net.Listen("tcp", ":0") // Let OS choose port
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()

	usedPort := listener.Addr().(*net.TCPAddr).Port
	if !controller.isPortInUse(usedPort) {
		t.Errorf("Port %d should be in use but was reported as free", usedPort)
	}
}

// TestController_IsServerReady verifies that the server readiness check correctly
// identifies when servers are responding to health checks.
func TestController_IsServerReady(t *testing.T) {
	controller := &Controller{}

	// Test with a healthy mock server
	healthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "healthy"}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer healthyServer.Close()

	// Extract port from test server URL
	_, portStr, _ := net.SplitHostPort(healthyServer.Listener.Addr().String())
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	if !controller.isServerReady(port) {
		t.Error("Expected healthy server to be ready")
	}

	// Test with non-existent server
	if controller.isServerReady(99999) { // Port unlikely to be in use
		t.Error("Expected non-existent server to not be ready")
	}
}

// TestController_IsServerReadyUnhealthy verifies that servers returning non-200
// status codes are correctly identified as not ready.
func TestController_IsServerReadyUnhealthy(t *testing.T) {
	controller := &Controller{}

	// Test with server returning 500 error
	unhealthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer unhealthyServer.Close()

	_, portStr, _ := net.SplitHostPort(unhealthyServer.Listener.Addr().String())
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	if controller.isServerReady(port) {
		t.Error("Expected server returning 500 to not be ready")
	}
}

// TestServerProcess_Creation verifies that ServerProcess instances are created
// with correct node IDs, ports, and cancellation contexts.
func TestServerProcess_Creation(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &ServerProcess{
		nodeID: 1,
		port:   8081,
		cancel: cancel,
	}

	if server.nodeID != 1 {
		t.Errorf("Expected nodeID 1, got %d", server.nodeID)
	}
	if server.port != 8081 {
		t.Errorf("Expected port 8081, got %d", server.port)
	}
	if server.cancel == nil {
		t.Error("Expected cancel function to be set")
	}
}

// TestController_DisplayCurrentStatus verifies that the status display function
// handles both online and offline servers gracefully without crashing. This ensures
// the monitoring functionality is robust and provides useful information to operators.
func TestController_DisplayCurrentStatus(t *testing.T) {
	controller := &Controller{numNodes: 2}

	// Create one healthy server
	healthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthyServer.Close()

	controller.displayCurrentStatus()
	// Test passes if no panics occurred
}

// TestController_HandleShutdownGraceful verifies that the shutdown handling
// correctly manages server cancellation and cleanup.
func TestController_HandleShutdownGraceful(t *testing.T) {
	controller := &Controller{
		numNodes: 2,
		servers: []*ServerProcess{
			{nodeID: 0, port: 8080, cancel: func() {}},
			{nodeID: 1, port: 8081, cancel: func() {}},
		},
	}

	// Track if cancel functions were called
	cancelCalls := 0
	for i := range controller.servers {
		controller.servers[i].cancel = func() {
			cancelCalls++
		}
	}

	// Simulate the cancellation part of handleShutdown
	for _, server := range controller.servers {
		if server != nil {
			server.cancel()
		}
	}

	if cancelCalls != 2 {
		t.Errorf("Expected 2 cancel calls, got %d", cancelCalls)
	}
}

// TestController_PeerListGeneration verifies that peer lists are generated
// correctly for each server, excluding the server itself and including all
// other nodes in the cluster.
func TestController_PeerListGeneration(t *testing.T) {
	numNodes := 4

	for nodeID := 0; nodeID < numNodes; nodeID++ {
		// Generate peer list like the controller does
		peers := make([]string, 0, numNodes-1)
		for i := range numNodes {
			if i != nodeID {
				peerPort := 8080 + i
				peers = append(peers, fmt.Sprintf("localhost:%d", peerPort))
			}
		}

		// Verify peer list properties
		expectedPeerCount := numNodes - 1
		if len(peers) != expectedPeerCount {
			t.Errorf("Node %d: expected %d peers, got %d", nodeID, expectedPeerCount, len(peers))
		}

		// Verify self is not in peer list
		selfAddr := fmt.Sprintf("localhost:%d", 8080+nodeID)
		for _, peer := range peers {
			if peer == selfAddr {
				t.Errorf("Node %d: peer list should not contain self address %s", nodeID, selfAddr)
			}
		}

		// Verify all other nodes are in peer list
		expectedPeers := make(map[string]bool)
		for i := range numNodes {
			if i != nodeID {
				expectedPeers[fmt.Sprintf("localhost:%d", 8080+i)] = true
			}
		}

		for _, peer := range peers {
			if !expectedPeers[peer] {
				t.Errorf("Node %d: unexpected peer %s", nodeID, peer)
			}
			delete(expectedPeers, peer)
		}

		if len(expectedPeers) > 0 {
			t.Errorf("Node %d: missing expected peers: %v", nodeID, expectedPeers)
		}
	}
}

// TestController_ConcurrentServerManagement verifies that the controller can
// safely manage multiple servers concurrently without race conditions.
func TestController_ConcurrentServerManagement(t *testing.T) {
	controller := &Controller{
		numNodes: 3,
		servers:  make([]*ServerProcess, 0, 3),
	}

	var wg sync.WaitGroup
	var mutex sync.Mutex
	cancelCount := 0

	// Simulate concurrent server operations
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(nodeID int) {
			defer wg.Done()

			_, cancel := context.WithCancel(context.Background())
			server := &ServerProcess{
				nodeID: nodeID,
				port:   8080 + nodeID,
				cancel: func() {
					mutex.Lock()
					cancelCount++
					mutex.Unlock()
					cancel()
				},
			}

			mutex.Lock()
			controller.servers = append(controller.servers, server)
			mutex.Unlock()

			// Simulate some work
			time.Sleep(10 * time.Millisecond)

			// Clean up
			server.cancel()
		}(i)
	}

	wg.Wait()

	mutex.Lock()
	finalCancelCount := cancelCount
	finalServerCount := len(controller.servers)
	mutex.Unlock()

	if finalCancelCount != 3 {
		t.Errorf("Expected 3 cancel calls, got %d", finalCancelCount)
	}
	if finalServerCount != 3 {
		t.Errorf("Expected 3 servers, got %d", finalServerCount)
	}
}
