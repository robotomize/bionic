package bionic

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultRunnerNumber     = 1
	DefaultAddr             = "localhost:53300"
	DefaultMaxExecutionTime = 30 * time.Second
)

var defaultOptions = ManagerOptions{
	runnerNumber:  DefaultRunnerNumber,
	addr:          DefaultAddr,
	executionTime: DefaultMaxExecutionTime,
}

type Manager struct {
	hooks  []BHookFunc
	taskCh chan interface{}
	opts   ManagerOptions
	cancel func()

	cMu     sync.RWMutex
	clients map[uuid.UUID]*Session
}

func NewManager(opt ...ManagerOption) *Manager {
	opts := defaultOptions
	for _, o := range opt {
		o.apply(&opts)
	}
	return &Manager{
		hooks:   []BHookFunc{},
		taskCh:  make(chan interface{}, 1),
		opts:    opts,
		cancel:  nil,
		cMu:     sync.RWMutex{},
		clients: map[uuid.UUID]*Session{},
	}
}

type ManagerOption interface {
	apply(*ManagerOptions)
}

type ManagerOptions struct {
	runnerNumber  int
	addr          string
	executionTime time.Duration
}

type funcManagerOptions struct {
	f func(*ManagerOptions)
}

func (o *funcManagerOptions) apply(opts *ManagerOptions) {
	o.f(opts)
}

func newFuncOptions(f func(*ManagerOptions)) *funcManagerOptions {
	return &funcManagerOptions{f: f}
}

func RunnersNumber(n int) ManagerOption {
	return newFuncOptions(func(o *ManagerOptions) {
		o.runnerNumber = n
	})
}

func ManagerAddr(a string) ManagerOption {
	return newFuncOptions(func(o *ManagerOptions) {
		o.addr = a
	})
}

func MaxExecutionTime(t time.Duration) ManagerOption {
	return newFuncOptions(func(o *ManagerOptions) {
		o.executionTime = t
	})
}

func (m *Manager) Serve() {
	runnerCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	for i := 0; i < m.opts.runnerNumber; i++ {
		m.runner(runnerCtx)
	}
}

func (m *Manager) Stop() {
	m.cancel()
}

func (m *Manager) runner(ctx context.Context) {
	go func() {
		for {
			select {
			case t := <-m.taskCh:
				_ = t
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *Manager) AddTask(t interface{}) {

}

func (m *Manager) DeleteTask() {

}

type BHookFunc func() error

func (m *Manager) RegisterHooks(h ...BHookFunc) {
	m.hooks = append(m.hooks, h...)
}

func (m *Manager) executeHooks() {
	for _, h := range m.hooks {
		if err := h(); err != nil {
			fmt.Printf(err.Error())
		}
	}
}

type Job struct {
	ID            uuid.UUID   `json:"id"`
	ExecutionTime int64       `json:"executionTime"`
	Payload       interface{} `json:"payload"`
}

const (
	ClientWorking uint32 = iota
	ClientWaiting
	ClientDisconnected
)

type Session struct {
	ID       uuid.UUID
	State    uint32
	Hostname string
}

func newSession() *Session {
	return &Session{
		ID:       uuid.New(),
		State:    ClientWaiting,
		Hostname: "",
	}
}

func (c *Session) getState() uint32 {
	return atomic.LoadUint32(&c.State)
}

func (c *Session) toWorking() {
	atomic.StoreUint32(&c.State, ClientWorking)
}

func (c *Session) toWaiting() {
	atomic.StoreUint32(&c.State, ClientWaiting)
}

func (c *Session) toDisconnected() {
	atomic.StoreUint32(&c.State, ClientDisconnected)
}

type BClientHandlerFunc func([]byte) error

type Client struct {
	handlers []BClientHandlerFunc
}

func NewClient() *Client {
	return &Client{handlers: []BClientHandlerFunc{}}
}

func (c *Client) RegisterHandlers(f ...BClientHandlerFunc) {
	c.handlers = append(c.handlers, f...)
}

func (c *Client) handle(payload []byte) {
	for _, c := range c.handlers {
		if err := c(payload); err != nil {
			fmt.Printf(err.Error())
		}
	}
}
