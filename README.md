# PubSub System Setup and Usage Guide

## Prerequisites

1. **Go Installation**: Ensure Go 1.21+ is installed
2. **Dependencies**: The system uses Gorilla WebSocket library

## Setup Instructions

### 1. Create Project Directory
```bash
mkdir pubsub-system
cd pubsub-system
```

### 2. Initialize Go Module
```bash
go mod init pubsub-system
```

### 3. Create Files
Save each artifact as the following files:
- `server.go` - Server implementation
- `main.go` - Control program  
- `client.go` - Client program
- `server_test.go` - Test file

### 4. Install Dependencies
```bash
go get github.com/gorilla/websocket
go mod tidy
```

## Running the System

### Step 1: Start the Server Cluster
```bash
# Start 3 nodes (servers will run on ports 8080, 8081, 8082)
go run main.go server.go 3

# Or start fewer nodes
go run main.go server.go 2
```

**Expected Output:**
```
2024/01/15 10:30:00 Starting server 0 on port 8080
2024/01/15 10:30:00 Starting server 1 on port 8081
2024/01/15 10:30:00 Starting server 2 on port 8082
```

### Step 2: Connect Clients
Open new terminals for each client:

```bash
# Terminal 2: Connect to node 0
go run client.go server.go 0

# Terminal 3: Connect to node 1  
go run client.go server.go 1

# Terminal 4: Connect to node 2
go run client.go server.go 2
```

## Usage Examples

### Basic Messaging
1. Type any message in a client terminal and press Enter
2. The message will appear in all connected clients across all nodes
3. Messages include timestamp, node ID, and sequence number

### Client Commands
- `/help` - Show available commands
- `/quit` - Disconnect from server
- `/status`, `/info` - Show connection status
- `/ping` - Send a test message via websocket
- `/stats` - Show statistics for connected node
- `/echo <message>` - Echo a message with prefix


### Automated Messages
When no clients are connected to a node:
- The node automatically sends random messages every 5-10 seconds
- Messages contain random integers 0-9

## Testing the System

### Run Unit Tests
```bash
go test
```

### Run with Coverage
```bash
go test -cover
```

### Run Benchmarks
```bash
go test -bench=.
```

### Integration Testing
```bash
# Run full test suite including integration tests
go test -v -timeout 30s
```