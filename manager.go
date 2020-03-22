package bionic

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/opentracing/opentracing-go/log"
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
	hooks map[string][]HookFunc

	opts   ManagerOptions
	cancel func()

	finCh   chan uuid.UUID
	eventCh chan struct{}

	mu       sync.RWMutex
	jobs     map[uuid.UUID]*Job
	sessions []*Session
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
				job := m.jobs[id]
				m.execHooks(job.Kind, job.BodyResponse)
				job.Status = JobStatusDeleted
				job.Trace = append(job.Trace, NewJobTrace([16]byte{}, JobStatusDeleted, nil))
			case <-m.eventCh:
				m.schedule()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *Dispatcher) schedule() {
	for _, job := range m.jobs {
		if job.Status == JobStatusWaiting && len(m.sessions) > 0 {
			n := fastrand.Uint32n(uint32(len(m.sessions) - 1))
			m.sessions[n].addJob(job)
		}
	}
}

func (m *Dispatcher) AddJob(j *Job) {
	j.Status = JobStatusWaiting
	j.Trace = append(j.Trace, NewJobTrace([16]byte{}, JobStatusWaiting, nil))
	m.jobs[j.ID] = j
	m.eventCh <- struct{}{}
}

func (m *Dispatcher) CancelJob(ID uuid.UUID) {

}

type HookFunc func([]byte) error

func (m *Dispatcher) AddHook(kind string, h ...HookFunc) {
	m.hooks[kind] = append(m.hooks[kind], h...)
}

func (m *Dispatcher) execHooks(kind string, payload []byte) {
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
	s.jobs = append(s.jobs, job)
	for _, j := range s.jobs {
		if j.Status == JobStatusWaiting {
			s.sendJob(j)
			j.Status = JobStatusWorking
			j.Trace = append(j.Trace, NewJobTrace(s.ID, JobStatusWorking, s.Conn.Socket.RemoteAddr()))
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
	s.connIncr = 0
}

func (s *Session) checkConnIncr() {
	if s.connIncr >= criticalConnIncr {
		// close connection
	}
	if s.connIncr > maxConnIncr && s.connIncr < criticalConnIncr {
		s.sendPing()
	}
}

func (s *Session) read(ctx context.Context) {
	go func() {
		var j *JobMessage
		for {
			select {
			case bytes := <-s.Conn.Read():
				kind := uint8(gjson.GetBytes(bytes, "proto.kind").Int())
				switch kind {
				case PongMessageKind:
					s.resetConnIncr()
				case JobCompletedMessageKind:
					if err := json.Unmarshal(bytes, &j); err != nil {
						log.Error(err)
					}
					for _, job := range s.jobs {
						if job.ID == j.Job.ID {
							job.BodyResponse = j.Job.Body
							job.Status = JobStatusFinished
							job.Trace = append(job.Trace, NewJobTrace(s.ID, JobStatusFinished, s.Conn.Socket.RemoteAddr()))
							s.finCh <- job.ID
						}
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Session) sendPing() {
	m := &PingMessage{Proto: Proto{SessionID: s.ID, Kind: PingMessageKind}}
	bytes, err := json.Marshal(&m)
	if err != nil {
		fmt.Printf(err.Error())
	}
	if err := s.Conn.Write(bytes); err != nil {
		fmt.Printf(err.Error())
	}
}

func (s *Session) sendJob(j *Job) {
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
		fmt.Printf(err.Error())
	}
	if err := s.Conn.Write(bytes); err != nil {
		fmt.Printf(err.Error())
	}
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
