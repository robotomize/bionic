package bionic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/opentracing/opentracing-go/log"
	"github.com/tidwall/gjson"
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

	finCh        chan Job
	jobs         []Job
	sessions     *Sessions
	sessionTasks map[uuid.UUID][]*Session
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
		finCh:    make(chan Job),
		cMu:      sync.RWMutex{},
		sessions: &Sessions{sessions: map[uuid.UUID]*Session{}},
	}
	return m
}

func (m *Manager) Serve() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Printf(err.Error())
		}
		conn := NewConn(socket)
		session := newSession()
		session.readCh = conn.wrCh
		session.finCh = m.finCh
		go conn.Read()
	})

	go func() {
		if err := http.ListenAndServe(":9090", nil); err != nil {
			fmt.Printf(err.Error())
		}
	}()
}

func (m *Manager) getAll() []Job {
	var jobs []Job
	jobs = append(jobs, m.jobs...)
	for _, j := range m.sessions.sessions {
		jobs = append(jobs, j.jobs...)
	}
	return jobs
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

func (m *Manager) Stop() {
	m.cancel()
}

func (m *Manager) handling(ctx context.Context) {
	go func() {
		for {
			select {
			case job := <-m.finCh:
				m.executeHooks(job.Kind, job.PayloadResponse)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *Manager) AddJob(j Job) {
	m.AddJob(j)
}

func (m *Manager) DeleteJob() {

}

func (m *Manager) GetSessions() *Sessions {
	return m.sessions
}

type HookFunc func([]byte) error

func (m *Manager) RegisterHook(kind string, h HookFunc) {
	m.hooks[kind] = append(m.hooks[kind], h)
}

func (m *Manager) executeHooks(kind string, payload []byte) {
	for _, f := range m.hooks[kind] {
		if err := f(payload); err != nil {
			fmt.Printf(err.Error())
		}
	}
}

const (
	JobStatusWaiting uint8 = iota
	JobStatusWorking
	JobStatusFinished
)

type JobTrace struct {
	sessionID uuid.UUID
	status    uint8
	timestamp int64
}

func NewJobTrace(sessionID uuid.UUID, status uint8) JobTrace {
	return JobTrace{sessionID: sessionID, status: status}
}

type Job struct {
	ID              uuid.UUID
	Status          uint32
	Kind            string
	ExecutionTime   int64
	Trace           []JobTrace
	PayloadRequest  []byte
	PayloadResponse []byte
}

func NewJob(kind string, reqPayload []byte, execTime int64) Job {
	return Job{
		ID:             uuid.New(),
		Kind:           kind,
		ExecutionTime:  execTime,
		PayloadRequest: reqPayload,
	}
}

const (
	SessionStatusWorking uint8 = iota
	SessionStatusWaiting
	SessionStatusDisconnected
	SessionStatusLocked
)

type Sessions struct {
	mu       sync.RWMutex
	sessions map[uuid.UUID]*Session
}

const (
	maxConnIncr      = 300
	criticalConnIncr = 600
	updateTimeValue  = 10
)

type Session struct {
	ID       uuid.UUID
	mu       sync.RWMutex
	jobs     []Job
	closeCh  chan uuid.UUID
	finCh    chan Job
	readCh   chan []byte
	Status   uint8
	connIncr int
	Conn     *Conn
}

func (s *Session) start() {

}

func (s *Session) stop() {

}

func (s *Session) addJob(job Job) {
	s.jobs = append(s.jobs, job)
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
		s.pushPing()
	}
}

func (s *Session) read(ctx context.Context) {
	go func() {
		var j *JobMessage
		for {
			select {
			case bytes := <-s.readCh:
				kind := uint8(gjson.GetBytes(bytes, "proto.kind").Int())
				switch kind {
				case PongKind:
					s.resetConnIncr()
				case JobCompletedKind:
					if err := json.Unmarshal(bytes, &j); err != nil {
						log.Error(err)
					}
					for _, job := range s.jobs {
						if job.ID == j.Job.ID {
							result := gjson.GetBytes(bytes, "job.payload").Raw
							job.PayloadResponse = []byte(result)
							job.Trace = append(job.Trace, NewJobTrace(s.ID, JobStatusFinished))
							s.finCh <- job
						}
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Session) pushPing() {
	m := &PingMessage{Proto: Proto{SessionID: s.ID, Kind: PingKind}}
	bytes, err := json.Marshal(&m)
	if err != nil {
		fmt.Printf(err.Error())
	}
	if err := s.writeRetry(3, bytes); err != nil {

	}
}

func (s *Session) pushJob(j *Job) {
	m := &JobMessage{
		Job: struct {
			ID      uuid.UUID   `json:"id"`
			Kind    string      `json:"kind"`
			Payload interface{} `json:"payload"`
		}{
			ID:      j.ID,
			Kind:    j.Kind,
			Payload: j.PayloadResponse,
		},
		Proto: Proto{
			SessionID: s.ID,
			Kind:      NewJobKind,
		},
	}
	bytes, err := json.Marshal(&m)
	if err != nil {
		fmt.Printf(err.Error())
	}
	if err := s.writeRetry(3, bytes); err != nil {

	}
}

func (s *Session) writeRetry(n int, bytes []byte) error {
	for i := 0; i < n; i++ {
		err := s.Conn.Write(bytes)
		if err == nil {
			return nil
		}
	}
	return errors.New("")
}

func newSession() *Session {
	return &Session{
		ID:       uuid.New(),
		Status:   SessionStatusWaiting,
		readCh:   make(chan []byte, 1),
		finCh:    make(chan Job, 1),
		jobs:     []Job{},
		connIncr: 0,
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
