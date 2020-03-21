package bionic

import "github.com/google/uuid"

const (
	PingKind uint8 = iota
	PongKind
	NewJobKind
	JobCompletedKind
)

type Proto struct {
	SessionID uuid.UUID `json:"sessionId"`
	Kind      uint8     `json:"kind"`
}

type PingMessage struct {
	Proto Proto `json:"proto"`
}

type JobMessage struct {
	Job struct {
		ID      uuid.UUID   `json:"id"`
		Kind    string      `json:"kind"`
		Payload interface{} `json:"payload"`
	} `json:"job"`
	Proto Proto `json:"proto"`
}
