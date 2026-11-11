// Copyright 2017 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package net

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// InternalListener is a listener that can be used to accept connections from
// other goroutines.
type InternalListener struct {
	acceptCh  chan net.Conn
	closed    bool
	mu        sync.Mutex
	accepting sync.WaitGroup
	closeOnce sync.Once
}

func NewInternalListener() *InternalListener {
	return &InternalListener{
		acceptCh: make(chan net.Conn, 128),
	}
}

func (l *InternalListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, fmt.Errorf("listener closed")
	}
	l.accepting.Add(1)
	l.mu.Unlock()
	defer l.accepting.Done()

	conn, ok := <-l.acceptCh
	if !ok {
		return nil, fmt.Errorf("listener closed")
	}
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		closePendingConn(conn)
		return nil, fmt.Errorf("listener closed")
	}
	return conn, nil
}

// PutConn transfers ownership on success. When the queue is full, it closes
// the connection and still returns nil; on error, the caller retains ownership.
func (l *InternalListener) PutConn(conn net.Conn) error {
	accepted, err := l.TryPutConn(conn)
	if err != nil {
		return err
	}
	if !accepted {
		_ = conn.Close()
	}
	return nil
}

// TryPutConn never closes conn. It transfers ownership only when accepted is
// true; on a full or closed listener, the caller retains ownership.
func (l *InternalListener) TryPutConn(conn net.Conn) (accepted bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false, fmt.Errorf("put conn error: listener is closed")
	}
	select {
	case l.acceptCh <- conn:
		return true, nil
	default:
		return false, nil
	}
}

func (l *InternalListener) Close() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		close(l.acceptCh)
		pending := make([]net.Conn, 0, len(l.acceptCh))
		for conn := range l.acceptCh {
			pending = append(pending, conn)
		}
		l.mu.Unlock()
		for _, conn := range pending {
			closePendingConn(conn)
		}
		// Accept may have received a queued connection immediately before
		// closure; wait until it either hands it off or closes it.
		l.accepting.Wait()
	})
	return nil
}

func closePendingConn(conn net.Conn) {
	// A wrapper's Close can flush buffered data; interrupt it on shutdown.
	_ = conn.SetDeadline(time.Now())
	_ = conn.Close()
}

func (l *InternalListener) Addr() net.Addr {
	return &InternalAddr{}
}

type InternalAddr struct{}

func (ia *InternalAddr) Network() string {
	return "internal"
}

func (ia *InternalAddr) String() string {
	return "internal"
}
