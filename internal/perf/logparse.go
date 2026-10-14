package perf

import (
	"strconv"
	"strings"
	"time"
)

// requestRecord is what the run reads from one line of the server's request
// log (internal/httpd/logging.go). Its duration is the server time of NFR-1
// to NFR-3: measured by the server from the moment the request reached its
// handler until the handler returned, with no network and no client in it.
type requestRecord struct {
	status   int
	duration time.Duration
	area     string
	decision string
	route    string
}

// parseRequestRecord reads a line written by slog's text handler. ok is false
// for every line that is not a request record: startup messages, warnings.
func parseRequestRecord(line string) (rec requestRecord, ok bool) {
	fields := logfmt(line)
	if fields["msg"] != "request" {
		return requestRecord{}, false
	}
	status, err := strconv.Atoi(fields["status"])
	if err != nil {
		return requestRecord{}, false
	}
	d, err := time.ParseDuration(fields["duration"])
	if err != nil {
		// slog's JSON handler writes nanoseconds; accept them too, should
		// the server's log format ever change.
		ns, nerr := strconv.ParseInt(fields["duration"], 10, 64)
		if nerr != nil {
			return requestRecord{}, false
		}
		d = time.Duration(ns)
	}
	return requestRecord{status: status, duration: d, area: fields["area"], decision: fields["decision"], route: fields["route"]}, true
}

// logfmt splits key=value pairs separated by spaces. A value that holds a
// space or a quote is written by slog as a Go quoted string.
func logfmt(line string) map[string]string {
	out := map[string]string{}
	for line != "" {
		line = strings.TrimLeft(line, " ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			break
		}
		key := line[:eq]
		line = line[eq+1:]
		var value string
		if strings.HasPrefix(line, `"`) {
			end := 1
			for end < len(line) && line[end] != '"' {
				if line[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(line) {
				break // a truncated line: nothing more to read
			}
			quoted := line[:end+1]
			line = line[end+1:]
			v, err := strconv.Unquote(quoted)
			if err != nil {
				v = quoted
			}
			value = v
		} else {
			sp := strings.IndexByte(line, ' ')
			if sp < 0 {
				sp = len(line)
			}
			value, line = line[:sp], line[sp:]
		}
		out[key] = value
	}
	return out
}
