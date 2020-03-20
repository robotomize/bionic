package bionic

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/opentracing/opentracing-go/log"
	"net/http"
	"runtime"
	"sync"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type WebsocketSessions struct {
	registerCh   chan *Conn
	unregisterCh chan *Conn
	data         chan []byte

	mu       sync.RWMutex
	clients  []*Conn
	sessions *Sessions
}

func NewWebsocketSessions(sessions *Sessions) *WebsocketSessions {
	return &WebsocketSessions{
		clients:      []*Conn{},
		data:         make(chan []byte, 1),
		sessions:     sessions,
		registerCh:   make(chan *Conn, 1),
		unregisterCh: make(chan *Conn, 1),
	}
}

func (h *WebsocketSessions) Accept() {
	go func() {
		for {
			select {
			case client := <-h.registerCh:
				h.onConnect(client)
			case client := <-h.unregisterCh:
				h.onDisconnect(client)
			}
		}
	}()
}

func (h *WebsocketSessions) Serve() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Printf(err.Error())
		}
		client := NewConn(h, socket)
		h.Register(client)
		go client.Read()
		go client.Write()
	})

	if err := http.ListenAndServe(":9090", nil); err != nil {
		fmt.Printf(err.Error())
	}
}

func (h *WebsocketSessions) Send(ID uuid.UUID, message []byte) {
	for _, c := range h.clients {
		if c.ID == ID {
			c.Send(message)
		}
	}
}

func (h *WebsocketSessions) Broadcast(message []byte) {
	for _, c := range h.GetClients() {
		if c != nil {
			c.Send(message)
		}
	}
}

func (h *WebsocketSessions) onMessage(data []byte) {
	h.data <- data
}

func (h *WebsocketSessions) Register(client *Conn) {
	h.registerCh <- client
}

func (h *WebsocketSessions) onConnect(client *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients = append(h.clients, client)
	h.sessions.sessions[client.ID] = newSession(client)
}

func (h *WebsocketSessions) onDisconnect(client *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	i := -1
	for j, c := range h.clients {
		if client.ID == c.ID {
			i = j
			break
		}
	}
	if i > -1 {
		copy(h.clients[i:], h.clients[i+1:])
		h.clients[len(h.clients)-1] = nil
		h.clients = h.clients[:len(h.clients)-1]
	}
	delete(h.sessions.sessions, client.ID)
	client.Close()
}

func (h *WebsocketSessions) GetClients() []*Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clients
}

type Conn struct {
	Connections *WebsocketSessions
	Socket      *websocket.Conn
	mu          sync.RWMutex
	ID          uuid.UUID
	userID      string
	outbound    chan []byte
	closed      bool
	doneCh      chan struct{}
	handlePanic bool
}

func NewConn(websocketSessions *WebsocketSessions, socket *websocket.Conn) *Conn {
	return &Conn{
		ID:          uuid.New(),
		Connections: websocketSessions,
		Socket:      socket,
		outbound:    make(chan []byte),
		doneCh:      make(chan struct{}),
		closed:      false,
	}
}

func (c *Conn) panicHandler() {
	if c.handlePanic {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			log.Error(fmt.Errorf("panic: %v\n%s", err, buf))
		}
	}
}

func (c *Conn) isClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

func (c *Conn) Send(data []byte) {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			log.Error(fmt.Errorf("panic: %v\n%s", err, buf))
		}
	}()
	c.outbound <- data
}

func (c *Conn) Read() {
	defer c.panicHandler()
	defer func() {
		c.Connections.unregisterCh <- c
	}()
	for {
		_, data, err := c.Socket.ReadMessage()
		if err != nil {
			log.Error(err)
			return
		}
		c.Connections.onMessage(data)
	}
}

func (c *Conn) Write() {
	defer c.panicHandler()
	for {
		select {
		case data, ok := <-c.outbound:
			if c.isClosed() {
				continue
			}
			if !ok {
				if err := c.Socket.WriteMessage(websocket.CloseMessage, []byte{}); err != nil {
					log.Error(err)
				}
				c.setClosed()
			}
			if err := c.Socket.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Error(err)
				c.setClosed()
			}
		case <-c.doneCh:
			return
		}
	}
}

func (c *Conn) setClosed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

func (c *Conn) Close() {
	if err := c.Socket.Close(); err != nil {
		log.Error(err)
	}
	defer close(c.outbound)
	defer close(c.doneCh)
	c.doneCh <- struct{}{}
}
