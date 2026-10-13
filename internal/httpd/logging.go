package httpd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Areas of the router, as they appear in the request log.
const (
	areaHostRejected = "host_rejected"
	areaHome         = "home"
	areaInternal     = "internal"
	areaReserved     = "reserved"
	areaResolver     = "resolver"
)

// maxLoggedName bounds the link name in a log record. A requested name comes
// from the URL and can be as long as the request line allows; a real name is
// at most 200 characters.
const maxLoggedName = 200

// logEntry collects what a request log record may say. It has no field for
// the raw path or the query on purpose: variable values and missed paths must
// not be recorded (NFR-17), and the surest way to keep them out of the log is
// to give them no place in it.
type logEntry struct {
	area     string
	decision string // resolver only
	link     string // resolver only: the matched or requested name, lowercase
	route    string // under "/-/" only: the matched mux pattern
}

// finishRequest returns the function that ends a request: it turns a panic
// into a 500 and writes the log record. Use as `defer s.finishRequest(...)()`
// so the clock starts when the request does.
func (s *Server) finishRequest(rec *statusRecorder, r *http.Request, entry *logEntry) func() {
	start := time.Now()
	return func() {
		if p := recover(); p != nil {
			if p == http.ErrAbortHandler {
				panic(p) // the documented way to abort a response; not a bug
			}
			s.log.Error("panic while serving a request",
				"area", entry.area, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			if !rec.wroteHeader {
				writeText(rec, r, http.StatusInternalServerError, "500 Internal Server Error\n")
			}
		}

		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.Int("status", rec.statusCode()),
			slog.Duration("duration", time.Since(start)),
			slog.String("area", entry.area),
		}
		if entry.decision != "" {
			attrs = append(attrs, slog.String("decision", entry.decision))
		}
		if entry.link != "" {
			attrs = append(attrs, slog.String("link", truncate(entry.link, maxLoggedName)))
		}
		if entry.route != "" {
			attrs = append(attrs, slog.String("route", entry.route))
		}
		if entry.area == areaHostRejected {
			// The refused name is what the operator needs for --allow-host.
			attrs = append(attrs, slog.String("host", truncate(r.Host, maxLoggedName)))
		}
		s.log.LogAttrs(context.WithoutCancel(r.Context()), levelFor(rec.statusCode(), entry.area), "request", attrs...)
	}
}

func levelFor(status int, area string) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case area == areaHostRejected:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// statusRecorder remembers the status code for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusRecorder) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = status, true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = http.StatusOK, true
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the real writer.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// statusCode is the status sent, or 200 when the handler wrote nothing, as
// net/http would send.
func (w *statusRecorder) statusCode() int {
	if !w.wroteHeader {
		return http.StatusOK
	}
	return w.status
}
