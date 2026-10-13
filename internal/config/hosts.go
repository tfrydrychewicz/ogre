package config

import (
	"errors"
	"fmt"
	"strings"
)

// hostList is the value of --allow-host: the flag may be repeated, and each
// use may carry several hosts separated by commas.
type hostList []string

// String implements flag.Value.
func (h *hostList) String() string {
	if h == nil {
		return ""
	}
	return strings.Join(*h, ",")
}

// Set implements flag.Value; every call adds to the list.
func (h *hostList) Set(value string) error {
	hosts := splitList(value)
	if len(hosts) == 0 {
		return errors.New("needs a host name")
	}
	*h = append(*h, hosts...)
	return nil
}

// splitList cuts a comma-separated list, dropping blanks.
func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// cleanHosts lowercases the allowed hosts, removes repeats and rejects
// entries that could never equal a Host header.
//
// The request guard compares these with the Host header as a whole, so a
// mistake here would not fail loudly later: the Directory would simply answer
// 403 to the name the user meant to allow. That is why a pasted URL or a
// wildcard is an error at startup.
func cleanHosts(hosts []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		switch {
		case strings.Contains(h, "://"):
			return nil, fmt.Errorf("%q is a URL; give only the host, such as \"go\" or \"go.example.com:8080\"", h)
		case strings.Contains(h, "*"):
			return nil, fmt.Errorf("%q: wildcards are not supported; list each host", h)
		case strings.ContainsAny(h, " \t\r\n/\\@?#"):
			return nil, fmt.Errorf("%q is not a host name; give a host, optionally with a port, such as \"go\" or \"go.example.com:8080\"", h)
		}
		seen[h] = true
		out = append(out, h)
	}
	return out, nil
}
