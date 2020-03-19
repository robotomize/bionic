package bionic

type PingMessage struct {
	State uint8
}

type ReceiveJobMessage struct {
	Job Job `json:"job"`
}

type SendCompletedMessage struct {
	Job     Job         `json:"job"`
	Payload interface{} `json:"payload"`
}
