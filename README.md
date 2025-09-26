# PubSub System Setup and Usage Guide

## Dependencies
```bash
go get github.com/gorilla/websocket
go mod tidy
```

## Running the System

### Step 1: Start the Server Cluster

You can start anywhere between 1 and 4 nodes using this system.

```bash
# Terminal 1: Start 3 nodes (servers will run on ports 8080, 8081, 8082)
go run server/main.go server/server.go 3
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
go run client/main.go 0

# Terminal 3: Connect to node 1  
go run client/main.go 1

# Terminal 4: Connect to node 2
go run client/main.go 2
```

## Usage Examples

### Basic Messaging

If you type in a string and press Enter, the message will be sent to every other online node and show up in their terminals.

### Client Commands
- `/help` - Show available commands
- `/quit` - Disconnect from server
- `/status`, `/info` - Show connection status
- `/ping` - Send a test message via websocket
- `/stats` - Show statistics for connected node
- `/echo <message>` - Echo a message with prefix


### Automated Messages
When no clients are connected to a node, the node automatically passes a random number to the network every 5-10 seconds.

## Testing the System

### Run Unit Tests
```bash
go test -v -cover ./...
```

### Run Benchmarks
```bash
go test -bench=./...
```
