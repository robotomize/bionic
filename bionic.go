package bionic

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"sync"
	"sync/atomic"
)

const (
	DefaultRunnerNumber = 1
	DefaultAddr         = "localhost:53300"
)

var defaultOptions = Options{
	runnerNumber: DefaultRunnerNumber,
	addr:         DefaultAddr,
}

type BHook func() error

type Manager struct {
	hooks  []BHook
	taskCh chan interface{}
	opts   Options
	cancel func()

	cMu     sync.RWMutex
	clients map[uuid.UUID]*Client
}

func New(opt ...Option) *Manager {
	opts := defaultOptions
	for _, o := range opt {
		o.apply(&opts)
	}
	return &Manager{
		hooks:   []BHook{},
		taskCh:  make(chan interface{}, 1),
		opts:    opts,
		cancel:  nil,
		cMu:     sync.RWMutex{},
		clients: map[uuid.UUID]*Client{},
	}
}

type Option interface {
	apply(*Options)
}

type Options struct {
	runnerNumber int
	addr         string
}

type funcOptions struct {
	f func(*Options)
}

func (o *funcOptions) apply(opts *Options) {
	o.f(opts)
}

func newFuncOptions(f func(*Options)) *funcOptions {
	return &funcOptions{f: f}
}

func SetRunnersNumber(n int) Option {
	return newFuncOptions(func(o *Options) {
		o.runnerNumber = n
	})
}

func SetAddr(addr string) Option {
	return newFuncOptions(func(o *Options) {
		o.addr = addr
	})
}

func (m *Manager) Run() {
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

func (m *Manager) RegisterHook(h ...BHook) {
	m.hooks = append(m.hooks, h...)
}

func (m *Manager) executeHooks() {
	for _, h := range m.hooks {
		if err := h(); err != nil {
			fmt.Printf(err.Error())
		}
	}
}

type Task struct {
}

const (
	ClientWorking uint32 = iota
	ClientWaiting
	ClientDisconnected
)

type Client struct {
	ID       uuid.UUID
	State    uint32
	Hostname string
}

func newClient() *Client {
	return &Client{
		ID:       uuid.New(),
		State:    ClientWaiting,
		Hostname: "",
	}
}

func (c *Client) getState() uint32 {
	return atomic.LoadUint32(&c.State)
}

func (c *Client) toWorking() {
	atomic.StoreUint32(&c.State, ClientWorking)
}

func (c *Client) toWaiting() {
	atomic.StoreUint32(&c.State, ClientWaiting)
}

func (c *Client) toDisconnected() {
	atomic.StoreUint32(&c.State, ClientDisconnected)
}
