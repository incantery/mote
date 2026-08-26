package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// wire is a scripted endpoint. It answers each request with the next
// stream in the script and keeps what it was sent, so a test can
// assert on the request as well as on the reply. No network: httptest
// is a socket on the loopback, and the whole exchange is in this
// process.
type wire struct {
	*httptest.Server

	mu      sync.Mutex
	replies []string
	bodies  []string
	headers []http.Header
	status  int
	failure string
}

// serve starts one, with a stream per expected request.
func serve(t *testing.T, replies ...string) *wire {
	t.Helper()
	w := &wire{replies: replies}
	w.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.mu.Lock()
		w.bodies = append(w.bodies, string(body))
		w.headers = append(w.headers, r.Header.Clone())
		status, failure, reply := w.status, w.failure, ""
		if n := len(w.bodies) - 1; n < len(w.replies) {
			reply = w.replies[n]
		}
		w.mu.Unlock()

		if status != 0 {
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(status)
			io.WriteString(rw, failure)
			return
		}
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.WriteHeader(http.StatusOK)
		flush(rw, reply)
	}))
	t.Cleanup(w.Close)
	return w
}

// stop makes the endpoint refuse everything from here on, the way one
// with a key it does not like would.
func (w *wire) stop(status int, body string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status, w.failure = status, body
}

// flush writes an event stream one blank-line-terminated event at a
// time, flushing after each: a test for streaming that delivered
// everything in one write would not have tested anything.
func flush(rw http.ResponseWriter, stream string) {
	f, _ := rw.(http.Flusher)
	for _, event := range strings.SplitAfter(stream, "\n\n") {
		if event == "" {
			continue
		}
		io.WriteString(rw, event)
		if f != nil {
			f.Flush()
		}
	}
}

// sent is the nth request body, decoded.
func (w *wire) sent(t *testing.T, n int) map[string]any {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if n >= len(w.bodies) {
		t.Fatalf("no request %d; there were %d", n, len(w.bodies))
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(w.bodies[n]), &body); err != nil {
		t.Fatalf("request %d is not JSON: %v\n%s", n, err, w.bodies[n])
	}
	return body
}

// raw is the nth request body as it was written.
func (w *wire) raw(n int) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if n >= len(w.bodies) {
		return ""
	}
	return w.bodies[n]
}

// header is one header of the nth request.
func (w *wire) header(n int, name string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if n >= len(w.headers) {
		return ""
	}
	return w.headers[n].Get(name)
}

func (w *wire) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.bodies)
}

// hang starts an endpoint that writes the start of a stream and then
// never finishes it, so a test can prove that cancelling the context
// is what ends it.
func hang(t *testing.T, start string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.WriteHeader(http.StatusOK)
		flush(rw, start)
		<-r.Context().Done()
	}))
	t.Cleanup(s.Close)
	return s
}

// collect gathers a stream's events, and runs whatever a test wants
// done as each one arrives.
type collect struct {
	text  strings.Builder
	think strings.Builder
	calls []Call
	fails []string
	each  func(Event)
}

func (c *collect) on(ev Event) {
	switch ev.Kind {
	case KindDelta:
		c.text.WriteString(ev.Text)
	case KindThinking:
		c.think.WriteString(ev.Text)
	case KindToolCall:
		c.calls = append(c.calls, ev.Call)
	case KindError:
		c.fails = append(c.fails, ev.Text)
	default:
		panic("provider: event with no kind")
	}
	if c.each != nil {
		c.each(ev)
	}
}
