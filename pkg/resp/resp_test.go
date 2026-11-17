package resp

import (
	"bufio"
	"bytes"
	"testing"
	"time"
)

func TestRESP_Parser(t *testing.T) {
	// Simple string
	r := bufio.NewReader(bytes.NewReader([]byte("+OK\r\n:100\r\n$5\r\nhello\r\n*2\r\n$4\r\necho\r\n$3\r\nbar\r\n")))
	v1, err := ReadValue(r)
	if err != nil || v1 != "OK" {
		t.Errorf("expected OK, got %v, err: %v", v1, err)
	}

	v2, err := ReadValue(r)
	if err != nil || v2 != int64(100) {
		t.Errorf("expected 100, got %v, err: %v", v2, err)
	}

	v3, err := ReadValue(r)
	if err != nil || v3 != "hello" {
		t.Errorf("expected hello, got %v, err: %v", v3, err)
	}

	v4, err := ReadValue(r)
	if err != nil {
		t.Fatalf("expected array, got err: %v", err)
	}
	arr, ok := v4.([]interface{})
	if !ok || len(arr) != 2 || arr[0] != "echo" || arr[1] != "bar" {
		t.Errorf("unexpected array: %v", arr)
	}
}

func TestClient_MockServer(t *testing.T) {
	ms, err := NewMockServer()
	if err != nil {
		t.Fatalf("NewMockServer failed: %v", err)
	}
	defer ms.Close()

	client := NewClient(ms.Addr(), 1*time.Second)
	defer client.Close()

	if err := client.Ping(); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	info, err := client.Info("replication")
	if err != nil {
		t.Fatalf("Info failed: %v", err)
	}
	if info["role"] != "master" || info["repl_backlog_size"] != "1048576" {
		t.Errorf("unexpected info replication: %v", info)
	}

	backlogSize, err := client.ConfigGetInt64("repl-backlog-size")
	if err != nil || backlogSize != 1048576 {
		t.Errorf("expected 1048576, got %d, err: %v", backlogSize, err)
	}

	if err := client.ConfigSet("repl-backlog-size", "2097152"); err != nil {
		t.Fatalf("ConfigSet failed: %v", err)
	}

	if ms.GetConfig("repl-backlog-size") != "2097152" {
		t.Errorf("expected updated config 2097152, got %s", ms.GetConfig("repl-backlog-size"))
	}
}
