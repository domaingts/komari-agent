package ws

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// SafeConn serializes application writes while allowing Close to interrupt a
// blocked write/read. Gorilla permits Close concurrently with other methods;
// keeping Close outside the write mutex is important for reader cleanup.
type SafeConn struct {
	conn      *websocket.Conn
	mu        sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

func NewSafeConn(conn *websocket.Conn) *SafeConn {
	return &SafeConn{conn: conn}
}

func (sc *SafeConn) WriteMessage(messageType int, data []byte) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.conn.WriteMessage(messageType, data)
}

func (sc *SafeConn) WriteJSON(v any) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.conn.WriteJSON(v)
}

func (sc *SafeConn) Close() error {
	sc.closeOnce.Do(func() {
		sc.closeErr = sc.conn.Close()
	})
	return sc.closeErr
}

func (sc *SafeConn) ReadMessage() (int, []byte, error) {
	return sc.conn.ReadMessage()
}

func (sc *SafeConn) ReadJSON(v any) error {
	return sc.conn.ReadJSON(v)
}

func (sc *SafeConn) SetReadDeadline(t time.Time) error {
	return sc.conn.SetReadDeadline(t)
}

func (sc *SafeConn) GetConn() *websocket.Conn {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.conn
}
