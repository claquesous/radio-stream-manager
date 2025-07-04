package types

import "time"

type StreamEvent struct {
  EventType string       `json:"event_type"`
  StreamID  int          `json:"stream_id"`
  Timestamp time.Time    `json:"timestamp"`
  Config    StreamConfig `json:"config"`
}

type StreamConfig struct {
  Name        string `json:"name"`
  Premium     bool   `json:"premium"`
  Description string `json:"description"`
  Genre       string `json:"genre"`
}

const (
  EventStreamCreated   = "stream_created"
  EventStreamUpdated   = "stream_updated"
  EventStreamDestroyed = "stream_destroyed"
)
