package box

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sonastea/chatterbox/internal/pkg/broker"
	"github.com/sonastea/chatterbox/internal/pkg/database"
	"github.com/sonastea/chatterbox/internal/pkg/store"
	"github.com/sonastea/chatterbox/internal/testutil"
	"github.com/sonastea/chatterbox/lib/chatterbox/message"
)

// Each operation sends a burst and waits for every delivery over real WebSockets.
// Setup, joins, and the initial delivery fence are outside b.Loop's timed region.
func BenchmarkWebSocketFanout(b *testing.B) {
	logs := testutil.CaptureLogs(b)
	b.Cleanup(func() {
		if b.Failed() {
			b.Logf("Server logs:\n%s", logs.String())
		}
	})
	for _, clients := range []int{2, 20} {
		for _, bodySize := range []int{64, 700} {
			b.Run(fmt.Sprintf("clients=%d/body=%d", clients, bodySize), func(b *testing.B) {
				db, err := database.Open(b.Context(), database.Config{URL: ":memory:"})
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { db.Close() })
				bus := broker.NewMemory()
				b.Cleanup(func() { bus.Close() })
				url, _ := testServer(b, b.Context(), db, bus)
				peers := make([]*websocket.Conn, clients)
				var room *Room
				for index := range peers {
					peers[index] = connect(b, url)
					room = join(b, peers[index], "benchmark")
				}
				// Drain join notices before counting newline-delimited chat deliveries.
				writeMessage(b, peers[0], Message{Type: message.Normal.String(), Room: room, Body: "ready"})
				for _, peer := range peers {
					readEvent(b, peer, message.SendMessage.String())
				}
				msg := Message{
					Type: message.Normal.String(), Room: &Room{Room: store.Room{Xid: room.Xid}},
					Body: strings.Repeat("x", bodySize),
				}
				payload := msg.encode()
				if len(payload) > maxMessageSize {
					b.Fatalf("benchmark input exceeds the frame limit: %d bytes", len(payload))
				}

				const burst = 32
				sends := 0
				b.ReportAllocs()
				for b.Loop() {
					// Rotate the sender while maintaining one writer per connection.
					sender := peers[(sends/burst)%len(peers)]
					sender.SetWriteDeadline(time.Now().Add(writeWait))
					for range burst {
						if err := sender.WriteMessage(websocket.TextMessage, payload); err != nil {
							b.Fatal(err)
						}
					}
					for _, peer := range peers {
						peer.SetReadDeadline(time.Now().Add(writeWait))
						remaining := burst
						for remaining > 0 {
							kind, data, err := peer.ReadMessage()
							if err != nil {
								b.Fatal(err)
							}
							if kind != websocket.TextMessage {
								b.Fatalf("unexpected WebSocket message type: %d", kind)
							}
							remaining -= bytes.Count(data, newLine) + 1
						}
						if remaining != 0 {
							b.Fatal("received more deliveries than sent")
						}
					}
					sends += burst
				}
				b.ReportMetric(float64(sends*clients)/b.Elapsed().Seconds(), "deliveries/s")
			})
		}
	}
}
