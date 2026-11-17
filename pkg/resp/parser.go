package resp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ReadValue parses a single RESP value from r.
func ReadValue(r *bufio.Reader) (interface{}, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(line, "\r\n")
	line = strings.TrimSuffix(line, "\n")
	if len(line) == 0 {
		return nil, errors.New("empty RESP line")
	}

	typeByte := line[0]
	payload := line[1:]

	switch typeByte {
	case '+': // Simple String
		return payload, nil
	case '-': // Error
		return nil, fmt.Errorf("redis error: %s", payload)
	case ':': // Integer
		return strconv.ParseInt(payload, 10, 64)
	case '$': // Bulk String
		length, err := strconv.Atoi(payload)
		if err != nil {
			return nil, fmt.Errorf("invalid bulk string length: %w", err)
		}
		if length == -1 {
			return nil, nil // Null bulk string
		}
		buf := make([]byte, length+2) // data + \r\n
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("read bulk string data: %w", err)
		}
		return string(buf[:length]), nil
	case '*': // Array
		count, err := strconv.Atoi(payload)
		if err != nil {
			return nil, fmt.Errorf("invalid array length: %w", err)
		}
		if count == -1 {
			return nil, nil // Null array
		}
		items := make([]interface{}, count)
		for i := 0; i < count; i++ {
			item, err := ReadValue(r)
			if err != nil {
				return nil, err
			}
			items[i] = item
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unknown RESP type: %q", typeByte)
	}
}

// EncodeCommand formats command arguments into a RESP array of bulk strings.
func EncodeCommand(args ...string) []byte {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("*%d\r\n", len(args)))
	for _, arg := range args {
		sb.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg))
	}
	return []byte(sb.String())
}
