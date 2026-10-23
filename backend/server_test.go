package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// streamFor serves an SSE-like stream that runs well past the server's
// short test timeouts and reports how many ticks it managed to send.
func streamFor(t *testing.T, lift bool) int {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lift {
			disableDeadlines(w)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 8; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			if _, err := fmt.Fprintf(w, "data: %d\n\n", i); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	})

	srv := newHTTPServer("", handler)
	// Same server, but with timeouts far shorter than the 800 ms stream.
	srv.ReadTimeout = 250 * time.Millisecond
	srv.WriteTimeout = 250 * time.Millisecond

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	ticks := 0
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data: ") {
			ticks++
		}
	}
	return ticks
}

func TestNewHTTPServerHasTimeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 {
		t.Fatalf("all timeouts must be set: %+v", srv)
	}
}

func TestDisableDeadlinesKeepsStreamAlive(t *testing.T) {
	if got := streamFor(t, true); got != 8 {
		t.Fatalf("stream with lifted deadlines sent %d of 8 events", got)
	}
}

// Control: proves the timeouts above really would cut the stream, so the
// test of disableDeadlines is meaningful.
func TestTimeoutsCutStreamWithoutLift(t *testing.T) {
	if got := streamFor(t, false); got >= 8 {
		t.Fatalf("expected the server timeouts to cut the stream, got all %d events", got)
	}
}
