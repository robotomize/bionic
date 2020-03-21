package bionic

import (
	"github.com/gorilla/websocket"
	"github.com/opentracing/opentracing-go/log"
	"net/http"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Conn struct {
	Socket *websocket.Conn
	wrCh   chan []byte
}

func NewConn(socket *websocket.Conn) *Conn {
	return &Conn{
		Socket: socket,
		wrCh:   make(chan []byte, 1),
	}
}

func (c *Conn) Read() {
	go func() {
		for {
			_, data, err := c.Socket.ReadMessage()
			if err != nil {
				log.Error(err)
				return
			}
			c.wrCh <- data
		}
	}()
}

func (c *Conn) Write(bytes []byte) error {
	return c.Socket.WriteMessage(websocket.TextMessage, bytes)
}

func (c *Conn) Close() {
	if err := c.Socket.Close(); err != nil {
		log.Error(err)
	}
}
