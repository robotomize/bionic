package bionic

import (
	"github.com/gorilla/websocket"
	"github.com/opentracing/opentracing-go/log"
)

type Conn struct {
	Socket *websocket.Conn
	wrCh   chan []byte
}

func NewConn(socket *websocket.Conn) *Conn {
	c := &Conn{
		Socket: socket,
		wrCh:   make(chan []byte, 1),
	}
	c.read()
	return c
}

func (c *Conn) Read() <-chan []byte {
	return c.wrCh
}

func (c *Conn) read() {
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
