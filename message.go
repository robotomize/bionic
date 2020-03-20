package bionic

import "github.com/google/uuid"

type Proto struct {
	ID   uuid.UUID `json:"id"`
	Kind uint8     `json:"kind"`
}

const (
	PingKind uint8 = iota
	PongKind
	NewJobKind
	JobCompletedKind
)

type PingMessage struct {
	Proto Proto `json:"proto"`
}

type JobMessage struct {
	Proto Proto `json:"proto"`
	Job   Job   `json:"job"`
}
