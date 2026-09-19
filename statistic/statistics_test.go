package statistic

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeAuthenticator struct {
	mu     sync.Mutex
	closed int
}

// Close 返回错误，用于验证释放路径会把错误传给调用方
func (f *fakeAuthenticator) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return errors.New("close failed")
}

func (f *fakeAuthenticator) AuthUser(string) (bool, User)                { return false, nil }
func (f *fakeAuthenticator) AddUser(string) error                        { return nil }
func (f *fakeAuthenticator) DelUser(string) error                        { return nil }
func (f *fakeAuthenticator) SetUserTraffic(string, uint64, uint64) error { return nil }
func (f *fakeAuthenticator) SetUserSpeedLimit(string, int, int) error    { return nil }
func (f *fakeAuthenticator) SetUserIPLimit(string, int) error            { return nil }
func (f *fakeAuthenticator) ListUsers() []User                           { return nil }

func (f *fakeAuthenticator) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

const fakeReleaseName = "FAKE_TEST_RELEASE"

func TestReleaseAuthenticatorClosesAndUnregisters(t *testing.T) {
	created := &fakeAuthenticator{}
	RegisterAuthenticatorCreator(fakeReleaseName, func(context.Context) (Authenticator, error) {
		return created, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	auth, err := NewAuthenticator(ctx, fakeReleaseName)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	if auth != Authenticator(created) {
		t.Fatal("unexpected authenticator returned")
	}
	if !registeredInCreatedAuth(ctx) {
		t.Fatal("NewAuthenticator did not register into createdAuth")
	}

	if err := ReleaseAuthenticator(ctx); err == nil {
		t.Fatal("ReleaseAuthenticator should propagate Close error")
	}
	if got := created.closeCount(); got != 1 {
		t.Fatalf("Close called %d times, want 1", got)
	}
	if registeredInCreatedAuth(ctx) {
		t.Fatal("createdAuth still holds the released instance")
	}

	// 重复释放必须是空操作：不二次 Close，也不 panic
	if err := ReleaseAuthenticator(ctx); err != nil {
		t.Fatalf("second ReleaseAuthenticator: %v", err)
	}
	if got := created.closeCount(); got != 1 {
		t.Fatalf("Close called %d times after repeated release, want 1", got)
	}
}

func TestReleaseAuthenticatorUnknownContext(t *testing.T) {
	if err := ReleaseAuthenticator(context.Background()); err != nil {
		t.Fatalf("releasing an unregistered context must be a no-op, got %v", err)
	}
}

func registeredInCreatedAuth(ctx context.Context) bool {
	createdAuthLock.Lock()
	defer createdAuthLock.Unlock()
	_, found := createdAuth[ctx]
	return found
}
