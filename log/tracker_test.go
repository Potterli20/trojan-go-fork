package log

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
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
	fields := map[string]any{
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
	for i := range numGoroutines {
		go func(id int) {
			defer wg.Done()
			tracker := NewConnectionTracker("Test", "Connect").
				WithField("goroutine", id)
			time.Sleep(time.Millisecond * time.Duration(id%10))
			if id%2 == 0 {
				if err := tracker.Success(); err != nil {
					errChan <- err
				}
			} else {
				if err := tracker.Error(errors.New("test error")); err != nil {
					errChan <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	errorCount := 0
	for range errChan {
		errorCount++
	}

	expectedErrors := numGoroutines / 2
	if errorCount != expectedErrors {
		t.Errorf("Expected %d errors, got %d", expectedErrors, errorCount)
	}
}

func TestGenerateConnIDUniqueness(t *testing.T) {
	idSet := make(map[string]bool)
	numIDs := 10000

	for range numIDs {
		id := generateConnID()
		if idSet[id] {
			t.Errorf("Duplicate connection ID found: %s", id)
		}
		idSet[id] = true
	}
}

// TestGenerateConnIDKeepsTheOldByteFormat 钉住格式本身。
// generateConnID 从 fmt.Sprintf 换成手工拼字节是为了省掉 fmt 的开销，但这类改写最容易
// 悄悄改掉的就是输出：前导零、进制、大小写、分隔符任何一处漂了，日志关联就断了，
// 而"唯一性"测试照样过。所以这里把字符串拆回两个整数、再用旧的那句 Sprintf 反向重建，
// 逐字节比对——只有格式完全一致才可能相等。
func TestGenerateConnIDKeepsTheOldByteFormat(t *testing.T) {
	for range 2000 {
		id := generateConnID()
		if !strings.HasPrefix(id, "conn-") {
			t.Fatalf("id %q 不是 conn- 前缀", id)
		}
		rest := strings.TrimPrefix(id, "conn-")
		nanosecondPart, counterPart, found := strings.Cut(rest, "-")
		if !found {
			t.Fatalf("id %q 缺少第二个字段分隔符", id)
		}
		n1, err1 := strconv.ParseInt(nanosecondPart, 10, 64)
		n2, err2 := strconv.ParseInt(counterPart, 10, 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("id %q 的两个字段不全是十进制整数: %v %v", id, err1, err2)
		}
		if n1 < 0 || n1 > 999999 {
			t.Fatalf("id %q 的纳秒尾数越界: %d", id, n1)
		}
		if rebuilt := fmt.Sprintf("conn-%d-%d", n1, n2); rebuilt != id {
			t.Fatalf("格式漂了：旧实现会写成 %q，现在是 %q", rebuilt, id)
		}
	}
}

// TestGenerateConnIDAllocationsBounded 是这条性能改写的反向门禁：fmt.Sprintf 那一版
// 实测 2 allocs / 40 B，手工拼字节 + 栈上 scratch 是 1 alloc / 23 B（唯一的堆分配就是
// 最终那个 string）。退回 fmt 会让这里立刻变红。
//
// scratch 数组必须留在栈上才吃得到这个收益，所以 cap 也不能随便调小：最大长度是
// 5 + 6 + 1 + 20 = 32 字节，实现取 40。cap 不够时 append 会扩容，分配数就回到 2 了。
func TestGenerateConnIDAllocationsBounded(t *testing.T) {
	// 先预热，避免把首次分配的惰性成本算进平均值
	for range 100 {
		_ = generateConnID()
	}
	n := testing.AllocsPerRun(2000, func() {
		_ = generateConnID()
	})
	if n > 1 {
		t.Fatalf("generateConnID 每次调用分配 %v 次，期望 ≤1（string 那一次）——"+
			"scratch 逃逸到堆上或 fmt 回来了", n)
	}
}

func TestConnectionTrackerDurationCalculation(t *testing.T) {
	SetLogLevelInternal(OffLevel)

	tracker := NewConnectionTracker("Test", "Connect")
	startTime := tracker.startTime

	time.Sleep(time.Millisecond * 50)
	_ = tracker.Success()

	duration := tracker.endTime.Sub(startTime)
	if duration < time.Millisecond*50 {
		t.Errorf("Expected duration >= 50ms, got %v", duration)
	}
}
