package resp

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
)

// MockServer is an in-memory Redis server for offline unit testing.
type MockServer struct {
	ln       net.Listener
	mu       sync.RWMutex
	running  bool
	infoData map[string]map[string]string
	config   map[string]string
}

// NewMockServer creates and starts a mock Redis server.
func NewMockServer() (*MockServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	ms := &MockServer{
		ln:       ln,
		running:  true,
		infoData: make(map[string]map[string]string),
		config:   make(map[string]string),
	}

	// Default info
	ms.infoData["replication"] = map[string]string{
		"role":                   "master",
		"connected_slaves":       "1",
		"master_replid":          "8f3a9c2b1d0e4f5a6b7c8d9e0f1a2b3c4d5e6f7a",
		"master_repl_offset":     "1000000",
		"repl_backlog_active":    "1",
		"repl_backlog_size":      "1048576", // 1MB
		"repl_backlog_first_byte_offset": "1",
		"repl_backlog_histlen":   "1000000",
	}

	ms.infoData["memory"] = map[string]string{
		"used_memory":            "536870912", // 512MB
		"used_memory_rss":        "603979776", // 576MB
		"maxmemory":              "1073741824", // 1GB
		"total_system_memory":    "8589934592", // 8GB
	}

	ms.infoData["server"] = map[string]string{
		"process_id":             strconv.Itoa(12345),
		"redis_version":          "7.2.4",
	}

	ms.config["repl-backlog-size"] = "1048576"
	ms.config["client-output-buffer-limit"] = "normal 0 0 0 slave 268435456 67108864 60 pubsub 33554432 8388608 60"

	go ms.serve()
	return ms, nil
}

// Addr returns the listener address.
func (ms *MockServer) Addr() string {
	return ms.ln.Addr().String()
}

// Close stops the server.
func (ms *MockServer) Close() error {
	ms.mu.Lock()
	ms.running = false
	ms.mu.Unlock()
	return ms.ln.Close()
}

// SetOffset updates master_repl_offset for testing velocity.
func (ms *MockServer) SetOffset(offset int64) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.infoData["replication"]["master_repl_offset"] = strconv.FormatInt(offset, 10)
}

// GetConfig returns current config value.
func (ms *MockServer) GetConfig(key string) string {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.config[key]
}

func (ms *MockServer) serve() {
	for {
		conn, err := ms.ln.Accept()
		if err != nil {
			ms.mu.RLock()
			running := ms.running
			ms.mu.RUnlock()
			if !running {
				return
			}
			continue
		}
		go ms.handleConn(conn)
	}
}

func (ms *MockServer) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		val, err := ReadValue(reader)
		if err != nil {
			return
		}

		arr, ok := val.([]interface{})
		if !ok || len(arr) == 0 {
			continue
		}

		cmd, _ := arr[0].(string)
		cmd = strings.ToUpper(cmd)

		var response []byte
		switch cmd {
		case "PING":
			response = []byte("+PONG\r\n")

		case "AUTH":
			response = []byte("+OK\r\n")

		case "INFO":
			section := ""
			if len(arr) > 1 {
				section, _ = arr[1].(string)
			}
			section = strings.ToLower(section)

			var sb strings.Builder
			ms.mu.RLock()
			if section == "" {
				for s, kv := range ms.infoData {
					sb.WriteString(fmt.Sprintf("# %s\r\n", strings.Title(s)))
					for k, v := range kv {
						sb.WriteString(fmt.Sprintf("%s:%s\r\n", k, v))
					}
					sb.WriteString("\r\n")
				}
			} else if kv, exists := ms.infoData[section]; exists {
				sb.WriteString(fmt.Sprintf("# %s\r\n", strings.Title(section)))
				for k, v := range kv {
					sb.WriteString(fmt.Sprintf("%s:%s\r\n", k, v))
				}
			}
			ms.mu.RUnlock()

			infoStr := sb.String()
			response = []byte(fmt.Sprintf("$%d\r\n%s\r\n", len(infoStr), infoStr))

		case "CONFIG":
			if len(arr) < 2 {
				response = []byte("-ERR wrong number of arguments for 'config' command\r\n")
				break
			}
			subCmd, _ := arr[1].(string)
			subCmd = strings.ToUpper(subCmd)

			if subCmd == "GET" && len(arr) >= 3 {
				key, _ := arr[2].(string)
				ms.mu.RLock()
				val := ms.config[key]
				ms.mu.RUnlock()
				response = []byte(fmt.Sprintf("*2\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n", len(key), key, len(val), val))
			} else if subCmd == "SET" && len(arr) >= 4 {
				key, _ := arr[2].(string)
				val, _ := arr[3].(string)
				ms.mu.Lock()
				ms.config[key] = val
				ms.mu.Unlock()
				response = []byte("+OK\r\n")
			} else {
				response = []byte("-ERR unknown or unsupported config subcommand\r\n")
			}

		default:
			response = []byte(fmt.Sprintf("-ERR unknown command '%s'\r\n", cmd))
		}

		if _, err := conn.Write(response); err != nil {
			return
		}
	}
}
