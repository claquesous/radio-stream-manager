# Radio Stream Manager

A Go-based service that manages ices streaming processes for multiple radio streams in an event-driven architecture.

## Architecture

This service receives stream lifecycle events via AWS SQS and manages ices processes accordingly:

- **Stream Created**: Spawns new ices process with unique configuration
- **Stream Updated**: Restarts ices process with updated configuration
- **Stream Destroyed**: Gracefully terminates ices process and cleans up

## Components

- **Rails API**: Publishes stream events to SQS queue
- **Stream Manager**: Go service that consumes events and manages processes
- **DynamoDB**: Stores stream process state and metadata
- **Ices**: Audio streaming processes (one per stream)
- **Icecast**: Audio server with multiple mountpoints

## Quick Start

### Prerequisites

- Go 1.21+ (install via asdf)
- ices2 package installed
- AWS credentials configured
- SQS queue and DynamoDB table set up

### Configuration

1. Edit `/etc/radio-stream-manager/config.yaml`:
```yaml
aws:
  region: "us-east-1"
  sqs_queue_url: "your-sqs-queue-url"
  dynamodb_table: "stream_processes"

icecast:
  host: "localhost"
  port: 8000
  password: "your-password"

api:
  base_url: "http://localhost:3000"
```

2. The system uses the included `ices-template.xml` as the ices template packaged with the Go service

3. Edit `/etc/radio-stream-manager/environment`:
```bash
AWS_REGION=us-east-1
STREAM_EVENTS_QUEUE_URL=https://sqs.us-east-1.amazonaws.com/account/stream-events
DYNAMODB_TABLE=stream_processes
API_BASE_URL=http://localhost:3000
ICECAST_PASSWORD=your_password
```

### Running

```bash
# Start service
sudo systemctl enable --now radio-stream-manager

# Check status
sudo systemctl status radio-stream-manager

# View logs
sudo journalctl -u radio-stream-manager -f
```

## Rails Integration

The Rails Stream model automatically publishes events using ActiveRecord callbacks:

```ruby
# Stream model callbacks
after_create :publish_stream_created
after_update :publish_stream_updated
before_destroy :publish_stream_destroyed
```

Events are published automatically when streams are created, updated, or destroyed through any method (controller, console, etc.).

## Stream Lifecycle

1. **Create Stream**: Rails publishes `stream_created` event
2. **Stream Manager**: Receives event, generates ices config, starts process
3. **Ices Process**: Connects to icecast, requests songs from Rails API
4. **Health Monitoring**: Regular heartbeats and process supervision
5. **Destroy Stream**: Rails publishes `stream_destroyed` event, process terminated

## Scaling

### Single Instance (Current)
- Supports 10-20 concurrent streams

### Container-Based (Phase 2)
- Horizontal scaling with Docker/ECS
- Auto-scaling based on stream count

### Microservices (Phase 3)
- Kubernetes orchestration
- Global CDN distribution
- Millions of streams

## Monitoring

### Logs
```bash
# Service logs
sudo journalctl -u radio-stream-manager

# Individual stream logs
tail -f /var/log/radio-stream-manager/stream-123/ices.log
```

### Process Status
```bash
# List running streams
ps aux | grep ices

# Check DynamoDB records
aws dynamodb scan --table-name stream_processes
```

## Development

### Building
```bash
go mod tidy
go build -o radio-stream-manager cmd/radio-stream-manager/main.go
```

### Testing Events
```bash
# Simulate stream creation
aws sqs send-message \
  --queue-url $QUEUE_URL \
  --message-body '{"event_type":"stream_created","stream_id":"test-123","timestamp":"2025-06-19T21:00:00Z","payload":{"name":"Test Stream","api_endpoint":"http://localhost:3000/streams/123"}}'
```

## Troubleshooting

### Common Issues

1. **Process won't start**: Check ices binary path and permissions
2. **No audio**: Verify icecast connection and mountpoint configuration
3. **Memory usage**: Monitor process count and resource limits
4. **SQS permissions**: Ensure IAM role has SQS and DynamoDB access

### Debug Mode
```bash
# Run with debug logging
sudo systemctl edit radio-stream-manager
# Add: Environment=LOG_LEVEL=debug
```

## Security

- Service runs as dedicated `radio` user
- Restricted filesystem access via systemd
- AWS credentials via IAM roles (recommended)
- Process isolation per stream
