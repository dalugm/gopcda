package opcda

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// Server represents a connection to an OPC DA server.
type Server struct {
	mu        sync.Mutex
	closed    bool
	closeDone chan struct{}
	closeErr  error
	conn      connection
	ctx       context.Context
	cancel    context.CancelFunc
}

// Connect establishes a session using ctx only for connection setup. After success,
// canceling ctx does not end the session. Always call Close to release it.
func Connect(ctx context.Context, cfg ServerConfig) (*Server, error) {
	return connect(ctx, cfg, dialDCOM)
}

func connect(
	ctx context.Context,
	cfg ServerConfig,
	dial func(context.Context, ServerConfig) (connection, error),
) (*Server, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := dial(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("opcda: connect %s: %w", cfg.Host, err)
	}
	if err := ctx.Err(); err != nil {
		cleanup, done := cleanupContext()
		defer done()
		return nil, errors.Join(err, conn.closeContext(cleanup))
	}
	ctx = context.WithoutCancel(ctx)
	lifetime, cancel := context.WithCancel(ctx)
	return &Server{conn: conn, ctx: lifetime, cancel: cancel}, nil
}

// AddGroup creates an active, persistent DA2 group. updateRate must be a positive
// whole number of milliseconds, at most 4294967295ms. deadband is a percentage
// between 0 and 100. RevisedUpdateRate reports the period accepted by the server.
func (s *Server) AddGroup(
	ctx context.Context,
	name string,
	updateRate time.Duration,
	deadband float32,
) (*Group, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	s.mu.Unlock()
	ctx, done := s.operationContext(ctx)
	defer done()
	if err := s.operationError(ctx); err != nil {
		return nil, err
	}
	if updateRate <= 0 || updateRate%time.Millisecond != 0 ||
		updateRate/time.Millisecond > math.MaxUint32 {
		return nil, fmt.Errorf(
			"opcda: invalid group update rate %s: expected whole milliseconds in [1, 4294967295]",
			updateRate,
		)
	}
	group, err := s.conn.addGroup(ctx, name, updateRate.Milliseconds(), deadband)
	if err != nil {
		return nil, fmt.Errorf("opcda: add group %q: %w", name, err)
	}
	group.server = s
	group.name = name
	return group, nil
}

func (s *Server) operationContext(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	if s.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

// GetServerStatus queries status with a per-call deadline/cancellation.
func (s *Server) GetServerStatus(ctx context.Context) (*ServerStatus, error) {
	ctx, done := s.operationContext(ctx)
	defer done()
	if err := s.operationError(ctx); err != nil {
		return nil, err
	}
	return s.conn.getServerStatus(ctx)
}

func (s *Server) operationError(ctx context.Context) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}
	return ctx.Err()
}

// Close cancels pending group calls and releases groups, activated server
// references, ping sets and RPC connections. It reports cleanup errors and is
// idempotent. Use a fresh context with a timeout for cleanup if the operation's
// context has been canceled.
func (s *Server) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		done := s.closeDone
		s.mu.Unlock()
		select {
		case <-done:
			return s.closeErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.closed = true
	s.closeDone = make(chan struct{})
	s.mu.Unlock()
	s.cancel()
	var err error
	if s.conn != nil {
		err = s.conn.closeContext(ctx)
	}
	s.mu.Lock()
	s.closeErr = err
	close(s.closeDone)
	s.mu.Unlock()
	return err
}

// ServerStatus holds OPC DA server status information.
type ServerStatus struct {
	StartTime      time.Time
	CurrentTime    time.Time
	LastUpdateTime time.Time
	VendorInfo     string
	ProductVersion string
	State          int
}
