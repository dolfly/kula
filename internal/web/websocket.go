package web

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type wsClient struct {
	conn   *websocket.Conn
	sendCh chan []byte
	paused bool
	mu     sync.Mutex
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	wsHandler := websocket.Handler(func(conn *websocket.Conn) {
		client := &wsClient{
			conn:   conn,
			sendCh: make(chan []byte, 64),
		}

		s.hub.regCh <- client
		defer func() {
			s.hub.unregCh <- client
			conn.Close()
		}()

		// Send initial current data
		if sample := s.collector.Latest(); sample != nil {
			data, err := json.Marshal(sample)
			if err == nil {
				websocket.Message.Send(conn, string(data))
			}
		}

		// Read pump (for pause/resume commands)
		go func() {
			for {
				var msg string
				if err := websocket.Message.Receive(conn, &msg); err != nil {
					s.hub.unregCh <- client
					return
				}

				var cmd struct {
					Action string `json:"action"`
				}
				if err := json.Unmarshal([]byte(msg), &cmd); err != nil {
					continue
				}

				client.mu.Lock()
				switch cmd.Action {
				case "pause":
					client.paused = true
				case "resume":
					client.paused = false
				}
				client.mu.Unlock()
			}
		}()

		// Write pump
		for {
			select {
			case data, ok := <-client.sendCh:
				if !ok {
					return
				}
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := websocket.Message.Send(conn, string(data)); err != nil {
					log.Printf("WebSocket write error: %v", err)
					return
				}
			}
		}
	})

	wsHandler.ServeHTTP(w, r)
}
