package bionic

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"github.com/valyala/fastrand"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

func New() *Dispatcher {
	manager := NewDispatcher(MaxExecutionTime(30 * time.Second))
	return manager
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

const (
	defaultRunnerNumber     = 1
	defaultMaxExecutionTime = 30 * time.Second
)

var defaultOptions = ManagerOptions{
	runnerNumber:  defaultRunnerNumber,
	executionTime: defaultMaxExecutionTime,
}

type Dispatcher struct {
	mu       sync.RWMutex
	jobs     map[uuid.UUID]*Job
	sessions []*Session
	hooks    map[string][]HookFunc

	finCh   chan uuid.UUID
	eventCh chan struct{}

	opts   ManagerOptions
	cancel func()
}

func NewDispatcher(opt ...ManagerOption) *Dispatcher {
	opts := defaultOptions
	for _, o := range opt {
		o.apply(&opts)
	}
	m := &Dispatcher{
		hooks:    map[string][]HookFunc{},
		opts:     opts,
		cancel:   nil,
		finCh:    make(chan uuid.UUID, 1),
		sessions: []*Session{},
		jobs:     map[uuid.UUID]*Job{},
		eventCh:  make(chan struct{}, 1),
	}
	return m
}

func (m *Dispatcher) Serve() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.handling(ctx)
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Printf(err.Error())
			return
		}
		conn := NewConn(socket)
		session := newSession(conn)
		session.finCh = m.finCh
		session.start()

		m.sessions = append(m.sessions, session)
		m.eventCh <- struct{}{}
	})

	go func() {
		if err := http.ListenAndServe(":9090", nil); err != nil {
			fmt.Printf(err.Error())
		}
	}()
}

func (m *Dispatcher) Stop() {
	m.cancel()
	for _, s := range m.sessions {
		s.stop()
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

func MaxExecutionTime(t time.Duration) ManagerOption {
	return newFuncOptions(func(o *ManagerOptions) {
		o.executionTime = t
	})
}

func (m *Dispatcher) handling(ctx context.Context) {
	go func() {
		for {
			select {
			case id := <-m.finCh:
				m.mu.RLock()
				job := m.jobs[id]
				m.mu.RUnlock()
				m.execHooks(job.Kind, job.BodyResponse)
				job.mu.Lock()
				job.toDeleted()
				job.Trace = append(job.Trace, NewJobTrace([16]byte{}, JobStatusDeleted, nil))
				job.mu.Unlock()
			case <-m.eventCh:
				m.schedule()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *Dispatcher) schedule() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if job.isWaiting() && len(m.sessions) > 0 {
			n := fastrand.Uint32n(uint32(len(m.sessions) - 1))
			m.sessions[n].addJob(job)
		}
	}
}

func (m *Dispatcher) AddJob(j *Job) {
	j.mu.Lock()
	j.toWaiting()
	j.Trace = append(j.Trace, NewJobTrace([16]byte{}, JobStatusWaiting, nil))
	j.mu.Unlock()
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.mu.Unlock()
	m.eventCh <- struct{}{}
}

func (m *Dispatcher) CancelJob(ID uuid.UUID) {

}

type HookFunc func([]byte) error

func (m *Dispatcher) AddHook(kind string, h ...HookFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks[kind] = append(m.hooks[kind], h...)
}

func (m *Dispatcher) execHooks(kind string, payload []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.hooks[kind]; ok {
		for _, h := range m.hooks[kind] {
			if err := h(payload); err != nil {
				fmt.Printf(err.Error())
			}
		}
	}
}

const (
	JobStatusWaiting uint32 = iota
	JobStatusWorking
	JobStatusFinished
	JobStatusCancelled
	JobStatusDeleted
)

type JobTrace struct {
	sessionID uuid.UUID
	status    uint32
	timestamp int64
	addr      net.Addr
}

func NewJobTrace(sessionID uuid.UUID, status uint32, addr net.Addr) JobTrace {
	return JobTrace{sessionID: sessionID, status: status, addr: addr}
}

type Job struct {
	ID     uuid.UUID
	Status uint32

	mu            sync.RWMutex
	Kind          string
	ExecutionTime int64
	Trace         []JobTrace
	BodyRequest   []byte
	BodyResponse  []byte
}

func NewJob(kind string, body []byte, execTime int64) *Job {
	return &Job{
		ID:            uuid.New(),
		Kind:          kind,
		ExecutionTime: execTime,
		BodyRequest:   body,
		Status:        JobStatusWaiting,
		Trace:         []JobTrace{},
	}
}

func (s *Job) isWaiting() bool {
	return atomic.LoadUint32(&s.Status) == JobStatusWaiting
}

func (s *Job) isWorking() bool {
	return atomic.LoadUint32(&s.Status) == JobStatusWorking
}

func (s *Job) isCancelled() bool {
	return atomic.LoadUint32(&s.Status) == JobStatusCancelled
}

func (s *Job) isFinished() bool {
	return atomic.LoadUint32(&s.Status) == JobStatusFinished
}

func (s *Job) isDeleted() bool {
	return atomic.LoadUint32(&s.Status) == JobStatusDeleted
}

func (s *Job) toWaiting() {
	atomic.StoreUint32(&s.Status, JobStatusWaiting)
}

func (s *Job) toWorking() {
	atomic.StoreUint32(&s.Status, JobStatusWorking)
}

func (s *Job) toCancelled() {
	atomic.StoreUint32(&s.Status, JobStatusCancelled)
}

func (s *Job) toFinished() {
	atomic.StoreUint32(&s.Status, JobStatusFinished)
}

func (s *Job) toDeleted() {
	atomic.StoreUint32(&s.Status, JobStatusDeleted)
}

const (
	SessionStatusWaiting uint32 = iota
	SessionStatusWorking
	SessionStatusDisconnected
	SessionStatusLocked
)

const (
	maxConnIncr      = 90
	criticalConnIncr = 300
	updateTimeValue  = 10
)

type Session struct {
	ID       uuid.UUID
	Status   uint32
	connIncr uint32

	evCh    chan struct{}
	finCh   chan uuid.UUID
	closeCh chan uuid.UUID

	mu   sync.RWMutex
	jobs []*Job
	Conn *Conn

	cancel func()
}

func (s *Session) start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.connIncrTimer(ctx)
	s.read(ctx)
}

func (s *Session) stop() {
	s.cancel()
	s.Conn.Close()
}

func (s *Session) addJob(job *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, job)
	for _, j := range s.jobs {
		if j.isWaiting() {
			if err := s.sendJob(j); err != nil {
				fmt.Printf(err.Error())
				return
			}
			j.toWorking()
			j.mu.Lock()
			j.Trace = append(j.Trace, NewJobTrace(s.ID, JobStatusWorking, s.Conn.Socket.RemoteAddr()))
			j.mu.Unlock()
		}
	}
}

func (s *Session) connIncrTimer(ctx context.Context) {
	go func() {
		t := time.NewTicker(updateTimeValue * time.Second)
		for {
			select {
			case <-t.C:
				atomic.AddUint32(&s.connIncr, updateTimeValue)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Session) resetConnIncr() {
	atomic.StoreUint32(&s.connIncr, 0)
}

func (s *Session) checkConnIncr() {
	if atomic.LoadUint32(&s.connIncr) >= criticalConnIncr {
		// close connection
	}
	if atomic.LoadUint32(&s.connIncr) > maxConnIncr && atomic.LoadUint32(&s.connIncr) < criticalConnIncr {
		if err := s.sendPing(); err != nil {
			fmt.Printf(err.Error())
		}
	}
}

func (s *Session) read(ctx context.Context) {
	go func() {
		for {
			select {
			case bytes := <-s.Conn.Read():
				s.handleIncoming(bytes)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Session) handleIncoming(bytes []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var j *JobMessage // @TODO candidate for syn.pool
	kind := uint8(gjson.GetBytes(bytes, "proto.kind").Int())
	switch kind {
	case PongMessageKind:
		s.resetConnIncr()
	case JobCompletedMessageKind:
		if err := json.Unmarshal(bytes, &j); err != nil {
			fmt.Printf(err.Error())
			return
		}
		for _, job := range s.jobs {
			if job.ID == j.Job.ID {
				job.mu.Lock()
				job.BodyResponse = j.Job.Body
				job.toFinished()
				job.Trace = append(job.Trace, NewJobTrace(s.ID, JobStatusFinished, s.Conn.Socket.RemoteAddr()))
				job.mu.Unlock()
				s.finCh <- job.ID
			}
		}
	}
}

func (s *Session) sendPing() error {
	m := &PingMessage{Proto: Proto{SessionID: s.ID, Kind: PingMessageKind}}
	bytes, err := json.Marshal(&m)
	if err != nil {
		return err
	}
	if err := s.Conn.Write(bytes); err != nil {
		return err
	}
	return nil
}

func (s *Session) sendJob(j *Job) error {
	m := &JobMessage{
		Job: struct {
			ID   uuid.UUID `json:"id"`
			Kind string    `json:"kind"`
			Body []byte    `json:"body"`
		}{
			ID:   j.ID,
			Kind: j.Kind,
			Body: j.BodyRequest,
		},
		Proto: Proto{
			SessionID: s.ID,
			Kind:      NewJobMessageKind,
		},
	}
	bytes, err := json.Marshal(&m)
	if err != nil {
		return err
	}
	if err := s.Conn.Write(bytes); err != nil {
		return err
	}
	return nil
}

func newSession(conn *Conn) *Session {
	return &Session{
		ID:       uuid.New(),
		Status:   SessionStatusWaiting,
		finCh:    make(chan uuid.UUID, 1),
		evCh:     make(chan struct{}, 1),
		jobs:     []*Job{},
		connIncr: 0,
		Conn:     conn,
	}
}

func (s *Session) isWorking() bool {
	return atomic.LoadUint32(&s.Status) == SessionStatusWorking
}

func (s *Session) isWaiting() bool {
	return atomic.LoadUint32(&s.Status) == SessionStatusWaiting
}

func (s *Session) isDisconnected() bool {
	return atomic.LoadUint32(&s.Status) == SessionStatusDisconnected
}

func (s *Session) isLocked() bool {
	return atomic.LoadUint32(&s.Status) == SessionStatusLocked
}

func (s *Session) toWorking() {
	atomic.StoreUint32(&s.Status, SessionStatusWorking)
}

func (s *Session) toWaiting() {
	atomic.StoreUint32(&s.Status, SessionStatusWaiting)
}

func (s *Session) toDisconnected() {
	atomic.StoreUint32(&s.Status, SessionStatusDisconnected)
}

func (s *Session) toLocked() {
	atomic.StoreUint32(&s.Status, SessionStatusLocked)
}
