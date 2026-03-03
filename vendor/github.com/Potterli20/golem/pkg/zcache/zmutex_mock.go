package zcache

import (
	"github.com/stretchr/testify/mock"
)

type MockZMutex struct {
	mock.Mock
}

func (m *MockZMutex) Lock() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockZMutex) Unlock() (bool, error) {
	args := m.Called()
	return args.Bool(0), args.Error(1)
}

func (m *MockZMutex) Name() string {
	args := m.Called()
	return args.String(0)
}
