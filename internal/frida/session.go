package frida

import (
	"errors"
	"sync"
)

type Message struct {
	Type    string
	Payload []byte
	Data    []byte
}
type Script interface {
	Unload() error
	Post([]byte) error
}
type Session interface {
	LoadScript(string) (Script, error)
	Detach() error
}
type Device interface{ Attach(uint32) (Session, error) }

type MockDevice struct {
	mu       sync.Mutex
	sessions map[uint32]*MockSession
}
type MockSession struct {
	mu            sync.Mutex
	loaded        bool
	detached      bool
	AgentMessages chan Message
}
type MockScript struct{ s *MockSession }

func NewMockDevice() *MockDevice { return &MockDevice{sessions: make(map[uint32]*MockSession)} }
func (d *MockDevice) Attach(pid uint32) (Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.sessions[pid]; ok {
		return nil, errors.New("already attached")
	}
	s := &MockSession{AgentMessages: make(chan Message, 16)}
	d.sessions[pid] = s
	return s, nil
}
func (s *MockSession) LoadScript(string) (Script, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.detached {
		return nil, errors.New("session detached")
	}
	s.loaded = true
	return &MockScript{s: s}, nil
}
func (s *MockSession) Detach() error { s.mu.Lock(); defer s.mu.Unlock(); s.detached = true; return nil }
func (m *MockScript) Unload() error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	m.s.loaded = false
	return nil
}
func (m *MockScript) Post(b []byte) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	if !m.s.loaded {
		return errors.New("script unloaded")
	}
	return nil
}
