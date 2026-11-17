package resp

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Client is a minimal, pure Go Redis client communicating over RESP2.
type Client struct {
	addr    string
	timeout time.Duration
	conn    net.Conn
	reader  *bufio.Reader
}

// NewClient creates a new RESP client.
func NewClient(addr string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		addr:    addr,
		timeout: timeout,
	}
}

// Connect dials the Redis server.
func (c *Client) Connect() error {
	conn, err := net.DialTimeout("tcp", c.addr, c.timeout)
	if err != nil {
		return err
	}
	c.conn = conn
	c.reader = bufio.NewReader(conn)
	return nil
}

// Close closes the connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Do executes a raw Redis command.
func (c *Client) Do(args ...string) (interface{}, error) {
	if c.conn == nil {
		if err := c.Connect(); err != nil {
			return nil, err
		}
	}

	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))

	payload := EncodeCommand(args...)
	if _, err := c.conn.Write(payload); err != nil {
		return nil, err
	}

	return ReadValue(c.reader)
}

// Auth sends the AUTH command.
func (c *Client) Auth(password string) error {
	if password == "" {
		return nil
	}
	_, err := c.Do("AUTH", password)
	return err
}

// Ping checks server connectivity.
func (c *Client) Ping() error {
	val, err := c.Do("PING")
	if err != nil {
		return err
	}
	if str, ok := val.(string); ok && strings.EqualFold(str, "PONG") {
		return nil
	}
	return fmt.Errorf("unexpected PING response: %v", val)
}

// Info parses output from the INFO command for a specific section.
func (c *Client) Info(section string) (map[string]string, error) {
	args := []string{"INFO"}
	if section != "" {
		args = append(args, section)
	}
	val, err := c.Do(args...)
	if err != nil {
		return nil, err
	}

	raw, ok := val.(string)
	if !ok {
		return nil, fmt.Errorf("expected string response from INFO, got %T", val)
	}

	infoMap := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			infoMap[parts[0]] = parts[1]
		}
	}

	return infoMap, nil
}

// ConfigGet retrieves a configuration parameter.
func (c *Client) ConfigGet(param string) (string, error) {
	val, err := c.Do("CONFIG", "GET", param)
	if err != nil {
		return "", err
	}

	arr, ok := val.([]interface{})
	if !ok || len(arr) < 2 {
		return "", fmt.Errorf("invalid CONFIG GET response: %v", val)
	}

	valStr, ok := arr[1].(string)
	if !ok {
		return "", fmt.Errorf("expected string config value, got %T", arr[1])
	}

	return valStr, nil
}

// ConfigGetInt64 retrieves an integer config value.
func (c *Client) ConfigGetInt64(param string) (int64, error) {
	valStr, err := c.ConfigGet(param)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(valStr, 10, 64)
}

// ConfigSet modifies a configuration parameter at runtime.
func (c *Client) ConfigSet(param, value string) error {
	val, err := c.Do("CONFIG", "SET", param, value)
	if err != nil {
		return err
	}
	if str, ok := val.(string); ok && strings.EqualFold(str, "OK") {
		return nil
	}
	return fmt.Errorf("unexpected CONFIG SET response: %v", val)
}
