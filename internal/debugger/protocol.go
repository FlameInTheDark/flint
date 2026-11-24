package debugger

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type message struct {
	Type       string          `json:"type"`
	Event      string          `json:"event"`
	RequestSeq int             `json:"request_seq"`
	Success    bool            `json:"success"`
	Message    string          `json:"message"`
	Body       json.RawMessage `json:"body"`
}

type protocol struct {
	conn    net.Conn
	events  chan message
	done    chan struct{}
	err     error // published by closing done
	mu      sync.Mutex
	seq     int
	pending map[int]chan message
}

func newProtocol(conn net.Conn, output func(string)) *protocol {
	p := &protocol{conn: conn, events: make(chan message, 64), done: make(chan struct{}), pending: make(map[int]chan message)}
	go func() {
		p.err = p.read(output)
		close(p.done)
	}()
	return p
}

func readMessage(r *bufio.Reader) (message, error) {
	var msg message
	size := 0
	for n := 0; ; n++ {
		line, err := r.ReadSlice('\n')
		if err != nil {
			return msg, err
		}
		if string(line) == "\r\n" {
			break
		}
		if n == 16 {
			return msg, fmt.Errorf("too many DAP headers")
		}
		key, value, ok := strings.Cut(string(line), ":")
		if ok && strings.EqualFold(key, "Content-Length") {
			size, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil || size <= 0 || size > 16<<20 {
				return msg, fmt.Errorf("invalid DAP content length: %q", strings.TrimSpace(value))
			}
		}
	}
	if size == 0 {
		return msg, fmt.Errorf("missing DAP content length")
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return msg, err
	}
	return msg, json.Unmarshal(body, &msg)
}

func (p *protocol) read(output func(string)) error {
	r := bufio.NewReader(p.conn)
	for {
		msg, err := readMessage(r)
		if err != nil {
			return err
		}
		switch msg.Type {
		case "response":
			p.mu.Lock()
			if ch := p.pending[msg.RequestSeq]; ch != nil {
				ch <- msg
				delete(p.pending, msg.RequestSeq)
			}
			p.mu.Unlock()
		case "event":
			switch msg.Event {
			case "output":
				var body struct{ Output string }
				if json.Unmarshal(msg.Body, &body) == nil {
					output(body.Output)
				}
			case "initialized", "stopped", "continued", "exited", "terminated", "breakpoint":
				select {
				case p.events <- msg:
				default:
					return fmt.Errorf("too many pending debugger events")
				}
			}
		}
	}
}

// send has a single caller (the session loop). The reader correlates replies
// while events arrive independently, including initialized before launch replies.
func (p *protocol) send(command string, args any) (int, <-chan message, error) {
	p.seq++
	id := p.seq
	body, err := json.Marshal(map[string]any{"seq": id, "type": "request", "command": command, "arguments": args})
	if err != nil {
		return id, nil, err
	}
	ch := make(chan message, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = fmt.Fprintf(p.conn, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return id, ch, err
}

func (p *protocol) forget(id int) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

func response(command string, msg message, into any) error {
	if !msg.Success {
		var body struct{ Error struct{ Format string } }
		_ = json.Unmarshal(msg.Body, &body)
		detail := body.Error.Format
		if detail == "" {
			detail = msg.Message
		}
		return fmt.Errorf("%s: %s", command, detail)
	}
	if into != nil {
		return json.Unmarshal(msg.Body, into)
	}
	return nil
}

func (p *protocol) call(ctx context.Context, command string, args, into any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id, ch, err := p.send(command, args)
	defer p.forget(id)
	if err != nil {
		return err
	}
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("%s: debugger did not respond", command)
	case msg := <-ch:
		return response(command, msg, into)
	case <-p.done:
		select {
		case msg := <-ch:
			return response(command, msg, into)
		default:
			return p.err
		}
	}
}
