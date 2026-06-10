package log

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewConnectionTracker(t *testing.T) {
	tracker := NewConnectionTracker("Test", "Connect")

	if tracker.connID == "" {
		t.Error("Connection ID should not be empty")
	}
	if tracker.module != "Test" {
		t.Errorf("Expected module 'Test', got '%s'", tracker.module)
	}
	if tracker.action != "Connect" {
		t.Errorf("Expected action 'Connect', got '%s'", tracker.action)
	}
	if tracker.startTime.IsZero() {
		t.Error("Start time should not be zero")
	}
}

func TestNewConnectionTrackerWithID(t *testing.T) {
	tracker := NewConnectionTrackerWithID("custom-id-123", "Test", "Connect")

	if tracker.connID != "custom-id-123" {
		t.Errorf("Expected connID 'custom-id-123', got '%s'", tracker.connID)
	}
}

func TestConnectionTrackerWithField(t *testing.T) {
	tracker := NewConnectionTracker("Test", "Connect").
		WithField("key1", "value1").
		WithField("key2", 123)

	if tracker.fields["key1"] != "value1" {
		t.Errorf("Expected field key1='value1', got '%v'", tracker.fields["key1"])
	}
	if tracker.fields["key2"] != 123 {
		t.Errorf("Expected field key2=123, got '%v'", tracker.fields["key2"])
	}
}

func TestConnectionTrackerWithFields(t *testing.T) {
	fields := map[string]interface{}{
		"host": "localhost",
		"port": 8080,
	}
	tracker := NewConnectionTracker("Test", "Connect").WithFields(fields)

	if tracker.fields["host"] != "localhost" {
		t.Errorf("Expected field host='localhost', got '%v'", tracker.fields["host"])
	}
	if tracker.fields["port"] != 8080 {
		t.Errorf("Expected field port=8080, got '%v'", tracker.fields["port"])
	}
}

func TestConnectionTrackerGetters(t *testing.T) {
	tracker := NewConnectionTracker("Test", "Connect")

	if tracker.ConnID() != tracker.connID {
		t.Error("ConnID() should return connID")
	}
	if tracker.StartTime() != tracker.startTime {
		t.Error("StartTime() should return startTime")
	}
}

func TestConnectionTrackerConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	numGoroutines := 100
	errChan := make(chan error, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			tracker := NewConnectionTracker("Test", "Connect").
				WithField("goroutine", id)
			time.Sleep(time.Millisecond * time.Duration(id%10))
			if id%2 == 0 {
				tracker.Success()
			} else {
				tracker.Error(errors.New("test error"))
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	errorCount := 0
	for range errChan {
		errorCount++
	}

	if errorCount != 0 {
		t.Errorf("Expected 0 errors, got %d", errorCount)
	}
}

func TestGenerateConnIDUniqueness(t *testing.T) {
	idSet := make(map[string]bool)
	numIDs := 10000

	for i := 0; i < numIDs; i++ {
		id := generateConnID()
		if idSet[id] {
			t.Errorf("Duplicate connection ID found: %s", id)
		}
		idSet[id] = true
	}
}

func TestConnectionTrackerDurationCalculation(t *testing.T) {
	SetLogLevelInternal(OffLevel)

	tracker := NewConnectionTracker("Test", "Connect")
	startTime := tracker.startTime

	time.Sleep(time.Millisecond * 50)
	tracker.Success()

	duration := tracker.endTime.Sub(startTime)
	if duration < time.Millisecond*50 {
		t.Errorf("Expected duration >= 50ms, got %v", duration)
	}
}
