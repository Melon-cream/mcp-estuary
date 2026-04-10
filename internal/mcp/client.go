package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

// MCPError is a protocol-level error returned by the upstream MCP server.
// Transport errors (broken pipe, closed client, etc.) are returned as plain errors.
type MCPError struct {
	Code    int
	Message string
}

func (e *MCPError) Error() string {
	return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message)
}

type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	cancel context.CancelFunc

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan Message
	closed  bool
	nextID  atomic.Uint64
}

func Start(ctx context.Context, cmd *exec.Cmd, stdout io.Reader, stdin io.WriteCloser) (*Client, error) {
	runCtx, cancel := context.WithCancel(ctx)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start process: %w", err)
	}
	client := &Client{
		cmd:     cmd,
		stdin:   stdin,
		cancel:  cancel,
		pending: make(map[string]chan Message),
	}
	go client.readLoop(runCtx, stdout)
	return client, nil
}

func (c *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo: ClientInfo{
			Name:    "mcp-estuary",
			Title:   "mcp-estuary gateway",
			Version: "0.1.0",
		},
	}
	var result InitializeResult
	if err := c.Call(ctx, "initialize", params, &result); err != nil {
		return result, err
	}
	if err := c.Notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	id := fmt.Sprintf("%d", c.nextID.Add(1))
	request := Notification(method, params)
	request.ID = json.RawMessage([]byte(id))

	waiter := make(chan Message, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("client is closed")
	}
	c.pending[id] = waiter
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.send(request); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case response, ok := <-waiter:
		if !ok {
			return errors.New("client closed")
		}
		if response.Error != nil {
			return &MCPError{Code: response.Error.Code, Message: response.Error.Message}
		}
		if out != nil && len(response.Result) > 0 {
			if err := json.Unmarshal(response.Result, out); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}
}

func (c *Client) Notify(ctx context.Context, method string, params any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return c.send(Notification(method, params))
	}
}

func (c *Client) drainPending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

func (c *Client) Close() error {
	c.drainPending()
	c.cancel()
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	return nil
}

func (c *Client) readLoop(ctx context.Context, reader io.Reader) {
	defer c.drainPending()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		switch {
		case HasID(msg.ID) && msg.Method != "" && len(msg.Result) == 0 && msg.Error == nil:
			_ = c.send(NewErrorResponse(msg.ID, -32601, "server-initiated requests are not supported"))
		case HasID(msg.ID):
			id := string(msg.ID)
			c.mu.Lock()
			waiter := c.pending[id]
			if waiter != nil && !c.closed {
				waiter <- msg
				delete(c.pending, id)
			}
			c.mu.Unlock()
		}
	}
}

func (c *Client) send(msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	return nil
}
