package bionic

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/opentracing/opentracing-go/log"
	"github.com/tidwall/gjson"
	"github.com/valyala/fastrand"
	"net"
	"net/http"
	"sync"
	"time"
)

func New() *Manager {
	manager := NewManager(MaxExecutionTime(30 * time.Second))
	return manager
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
	hooks map[string][]HookFunc

	opts   ManagerOptions
	cancel func()

	cMu sync.RWMutex

	finCh chan uuid.UUID
	jobs  map[uuid.UUID]*Job

	eventCh chan struct{}

	sessions []*Session
}

func NewManager(opt ...ManagerOption) *Manager {
	opts := defaultOptions
	for _, o := range opt {
		o.apply(&opts)
	}
	m := &Manager{
		hooks:    map[string][]HookFunc{},
		opts:     opts,
		cancel:   nil,
		finCh:    make(chan uuid.UUID, 1),
		cMu:      sync.RWMutex{},
		sessions: []*Session{},
		jobs:     map[uuid.UUID]*Job{},
		eventCh:  make(chan struct{}, 1),
	}
	return m
}

func (m *Manager) Serve() {
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
		conn.Read()
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

func (m *Manager) Stop() {
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

func (m *Manager) handling(ctx context.Context) {
	go func() {
		for {
			select {
			case id := <-m.finCh:
				job := m.jobs[id]
				m.executeHooks(job.Kind, job.PayloadResponse)
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

func (m *Manager) schedule() {
	for _, job := range m.jobs {
		if job.Status == JobStatusWaiting && len(m.sessions) > 0 {
			n := fastrand.Uint32n(uint32(len(m.sessions) - 1))
			m.sessions[n].addJob(job)
		}
	}
}

func (m *Manager) AddJob(j *Job) {
	j.Status = JobStatusWaiting
	j.Trace = append(j.Trace, NewJobTrace([16]byte{}, JobStatusWaiting, nil))
	m.jobs[j.ID] = j
	m.eventCh <- struct{}{}
}

func (m *Manager) DeleteJob() {

}

func (m *Manager) GetSessions() []*Session {
	return m.sessions
}

type HookFunc func([]byte) error

func (m *Manager) Register(kind string, h ...HookFunc) {
	m.hooks[kind] = append(m.hooks[kind], h...)
}

func (m *Manager) executeHooks(kind string, payload []byte) {
	if _, ok := m.hooks[kind]; ok {
		for _, h := range m.hooks[kind] {
			if err := h(payload); err != nil {
				fmt.Printf(err.Error())
			}
		}
	}
}

const (
	JobStatusWaiting uint8 = iota
	JobStatusWorking
	JobStatusFinished
	JobStatusDeleted
)

type JobTrace struct {
	sessionID uuid.UUID
	status    uint8
	timestamp int64
	addr      net.Addr
}

func NewJobTrace(sessionID uuid.UUID, status uint8, addr net.Addr) JobTrace {
	return JobTrace{sessionID: sessionID, status: status, addr: addr}
}

type Job struct {
	ID              uuid.UUID
	Status          uint8
	Kind            string
	ExecutionTime   int64
	Trace           []JobTrace
	PayloadRequest  []byte
	PayloadResponse []byte
}

func NewJob(kind string, reqPayload []byte, execTime int64) *Job {
	return &Job{
		ID:             uuid.New(),
		Kind:           kind,
		ExecutionTime:  execTime,
		PayloadRequest: reqPayload,
		Status:         JobStatusWaiting,
	}
}

const (
	SessionStatusWorking uint8 = iota
	SessionStatusWaiting
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
	mu       sync.RWMutex
	jobs     []*Job
	closeCh  chan uuid.UUID
	evCh     chan struct{}
	finCh    chan uuid.UUID
	Status   uint8
	connIncr int
	Conn     *Conn
	cancel   func()
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
				s.connIncr += updateTimeValue
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
			case bytes := <-s.Conn.wrCh:
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
							job.PayloadResponse = j.Job.Payload
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
			ID      uuid.UUID `json:"id"`
			Kind    string    `json:"kind"`
			Payload []byte    `json:"payload"`
		}{
			ID:      j.ID,
			Kind:    j.Kind,
			Payload: j.PayloadRequest,
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

func (s *Session) toWorking() {
	s.Status = SessionStatusWorking
}

func (s *Session) toWaiting() {
	s.Status = SessionStatusWaiting
}

func (s *Session) toDisconnected() {
	s.Status = SessionStatusDisconnected
}

func (s *Session) toLocked() {
	s.Status = SessionStatusLocked
}
