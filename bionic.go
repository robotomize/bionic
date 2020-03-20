package bionic

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const DefaultAddr = 53300

func New() {
	manager := NewManager(RunnersNumber(1), MaxExecutionTime(30*time.Second))
	sessions := manager.GetSessions()
	websocketSessions := NewWebsocketSessions(sessions)
	websocketSessions.Accept()
	websocketSessions.Serve()
	manager.Serve()
}

const (
	DefaultRunnerNumber     = 1
	DefaultMaxExecutionTime = 30 * time.Second
)

var defaultOptions = ManagerOptions{
	runnerNumber:  DefaultRunnerNumber,
	executionTime: DefaultMaxExecutionTime,
}

type Manager struct {
	ws    *WebsocketSessions
	httpd http.Server

	hooks  map[string][]BHookFunc
	taskCh chan interface{}
	opts   ManagerOptions
	cancel func()

	cMu sync.RWMutex

	sessions     *Sessions
	sessionTasks map[uuid.UUID][]*Session
}

func NewManager(opt ...ManagerOption) *Manager {
	opts := defaultOptions
	for _, o := range opt {
		o.apply(&opts)
	}
	m := &Manager{
		hooks:    map[string][]BHookFunc{},
		taskCh:   make(chan interface{}, 1),
		opts:     opts,
		cancel:   nil,
		cMu:      sync.RWMutex{},
		sessions: &Sessions{sessions: map[uuid.UUID]*Session{}},
	}
	return m
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

func (m *Manager) incoming(ctx context.Context) {
	go func() {
		var job Job
		for {
			select {
			case data := <-m.ws.data:
				if err := json.Unmarshal(data, &job); err != nil {
					fmt.Printf(err.Error())
				}
				m.executeHooks(job.Kind, data)
			case <-ctx.Done():
				return
			}
		}
	}()
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

func (m *Manager) GetSessions() *Sessions {
	return m.sessions
}

type BHookFunc func([]byte) error

func (m *Manager) RegisterHooks(kind string, h ...BHookFunc) {
	m.hooks[kind] = append(m.hooks[kind], h...)
}

func (m *Manager) executeHooks(kind string, payload []byte) {
	for _, f := range m.hooks[kind] {
		if err := f(payload); err != nil {
			fmt.Printf(err.Error())
		}
	}
}

type Job struct {
	ID            uuid.UUID   `json:"id"`
	Kind          string      `json:"name"`
	ExecutionTime int64       `json:"executionTime"`
	Payload       interface{} `json:"payload"`
}

func NewJob(kind string, payload interface{}, execTime int64) Job {
	return Job{
		ID:            uuid.New(),
		Kind:          kind,
		ExecutionTime: execTime,
		Payload:       payload,
	}
}

const (
	ClientWorking uint32 = iota
	ClientWaiting
	ClientDisconnected
)

type Sessions struct {
	mu       sync.RWMutex
	sessions map[uuid.UUID]*Session
}

type Session struct {
	ID     uuid.UUID
	Status uint32
	Conn   *Conn
}

func (s *Session) checkConn() {
	s.Conn.Send()
}

func newSession(conn *Conn) *Session {
	return &Session{
		ID:     uuid.New(),
		Status: ClientWaiting,
		Conn:   conn,
	}
}

func (s *Session) getStatus() uint32 {
	return atomic.LoadUint32(&s.Status)
}

func (s *Session) toWorking() {
	atomic.StoreUint32(&s.Status, ClientWorking)
}

func (s *Session) toWaiting() {
	atomic.StoreUint32(&s.Status, ClientWaiting)
}

func (s *Session) toDisconnected() {
	atomic.StoreUint32(&s.Status, ClientDisconnected)
}
