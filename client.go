package bionic

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"net/http"
)

type HandlerFunc func(*JobMessage) error

type Client struct {
	Conn     *Conn
	handlers map[string]HandlerFunc
}

func NewClient(url string, header http.Header) (*Client, error) {
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		return nil, err
	}
	return &Client{Conn: NewConn(conn), handlers: map[string]HandlerFunc{}}, nil
}

func (c *Client) Send(bytes []byte) error {
	return c.Conn.Write(bytes)
}

func (c *Client) Listen() {
	for p := range c.Conn.Read() {
		c.handle(p)
	}
}

func (c *Client) RegisterHandlers(kind string, f HandlerFunc) {
	c.handlers[kind] = f
}

func (c *Client) handle(payload []byte) {
	kind := uint8(gjson.GetBytes(payload, "proto.kind").Int())
	sessionId := gjson.GetBytes(payload, "proto.sessionId").String()

	id, err := uuid.Parse(sessionId)
	if err != nil {
		fmt.Printf(err.Error())
	}
	proto := Proto{
		SessionID: id,
		Kind:      kind,
	}
	switch kind {
	case PingMessageKind:
		pong := &PingMessage{Proto: proto}
		bytes, err := json.Marshal(pong)
		if err != nil {
			fmt.Printf(err.Error())
		}
		if err := c.Send(bytes); err != nil {
			fmt.Printf(err.Error())
		}
		return
	case NewJobMessageKind:
		job := &JobMessage{}
		if err := json.Unmarshal(payload, &job); err != nil {
			fmt.Printf(err.Error())
		}
		handler := c.handlers[job.Job.Kind]
		err := handler(job)
		if err != nil {
			fmt.Printf(err.Error())
			return
		}
		bytes, err := json.Marshal(&job)
		if err != nil {
			fmt.Printf(err.Error())
		}
		if err := c.Send(bytes); err != nil {
			fmt.Printf(err.Error())
		}
	}
}
