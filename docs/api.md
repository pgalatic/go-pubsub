# PubSub System REST API Documentation

## Server Endpoints

Each server node exposes the following HTTP endpoints:

### 1. WebSocket Connection
```
GET /ws
```
- **Purpose**: Establish WebSocket connection
- **Protocol**: WebSocket upgrade
- **Usage**: Used by clients to connect and send/receive messages in real-time

### 2. Message API (Inter-Server Communication)

```
POST /api/message
Content-Type: application/json
```

**Purpose**: Accept messages from other server nodes in the cluster
**Request Body**:
```json
{
  "id": "unique-message-id",
  "content": "Message content",
  "timestamp": "2024-01-15T10:30:45Z",
  "node_id": 0
}
```

**Response**:
- `200 OK` - Message processed successfully
- `400 Bad Request` - Invalid JSON format
- `405 Method Not Allowed` - Non-POST request

**Example**:
```bash
curl -X POST http://localhost:8080/api/message \
  -H "Content-Type: application/json" \
  -d '{
    "id": "msg-001",
    "content": "Hello from another node",
    "timestamp": "2024-01-15T10:30:45Z",
    "node_id": 1,
    "sequence": 100
  }'
```

### 3. Health Check
```
GET /health
```

**Purpose**: Check server status and get metrics

**Response**:
```json
{
  "node_id": 0,
  "port": 8080,
  "clients": 2,
  "timestamp": "2024-01-15T10:30:45Z"
}
```

**Example**:
```bash
curl http://localhost:8080/health
```

## Message Format

All messages in the system use this standardized format:

```json
{
  "id": "unique-message-identifier",
  "content": "actual-message-content", 
  "timestamp": "ISO-8601-timestamp",
  "node_id": 0
}
```

**Fields**:
- `id`: Unique identifier for deduplication (string)
- `content`: The actual message content (string)  
- `timestamp`: When the message was created (ISO-8601 format)
- `node_id`: ID of the originating server node (integer)

## WebSocket Message Flow

### Client to Server
When a client sends a message via WebSocket:

```json
{
  "content": "User message content"
}
```

The server automatically adds metadata:
- Generates unique `id`
- Sets current `timestamp`
- Adds server `node_id`

### Server to Client
All messages sent to clients include full metadata:

```json
{
  "id": "client-0-1705312245000000000",
  "content": "User message content",
  "timestamp": "2024-01-15T10:30:45Z",
  "node_id": 0
}
```

## Inter-Server Communication

Servers communicate with each other using the `/api/message` endpoint to ensure messages are delivered to all clients across the cluster.

**Message Flow**:
1. Client sends message to Server A via WebSocket
2. Server A broadcasts to its local clients
3. Server A forwards message to Server B and C via POST `/api/message`
4. Server B and C receive message and broadcast to their local clients
5. Duplicate messages are prevented using the unique `id` field

## Deduplication Strategy

The system implements "send at most once" semantics:

- Each message has a unique `id` field
- Servers maintain an in-memory buffer of recently seen message IDs
- When receiving a message via REST API, servers check if they've already processed it
- Old messages are periodically cleaned from the buffer (every 5 minutes)

## Error Handling

### WebSocket Errors
- Connection drops are handled gracefully
- Disconnected clients are automatically removed from broadcast lists
- Failed message sends to individual clients don't affect others

### HTTP API Errors
- Network failures between servers are logged but don't stop message processing
- Failed REST calls are retried automatically by the HTTP client timeout
