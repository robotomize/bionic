package bionic

import (
	"context"
	"github.com/labstack/gommon/log"
)

const (
	DefaultRunnerNumber = 1
)

type BHook func() error

type Manager struct {
	hooks  []BHook
	taskCh chan interface{}
	cfg    *ManagerConfig
	cancel func()
}

type ManagerConfig struct {
	runnerNumber int
}

func New() *Manager {
	return &Manager{
		cfg: NewConfig(DefaultRunnerNumber),
	}
}

func NewConfig(runnerNumber int) *ManagerConfig {
	return &ManagerConfig{
		runnerNumber: runnerNumber,
	}
}

func (m *Manager) AddCustomConfig(cfg *ManagerConfig) {
	m.cfg = cfg
}

func (m *Manager) Run() {
	if m.cfg == nil {
		m.cfg = NewConfig(DefaultRunnerNumber)
	}
	m.taskCh = make(chan interface{}, 1)
	runnerCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	for i := 0; i < m.cfg.runnerNumber; i++ {
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
			log.Error(err)
		}
	}
}
