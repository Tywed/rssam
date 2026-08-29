package proxy

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParseStaticProxy parses host:port or socks5://[user:pass@]host:port into Proxy.
func ParseStaticProxy(raw string) (Proxy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Proxy{}, fmt.Errorf("proxy: static proxy is empty")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return Proxy{}, fmt.Errorf("proxy: invalid static proxy URL: %w", err)
		}
		host := u.Hostname()
		portStr := u.Port()
		if host == "" || portStr == "" {
			return Proxy{}, fmt.Errorf("proxy: static proxy URL must include host and port")
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 {
			return Proxy{}, fmt.Errorf("proxy: invalid static proxy port")
		}
		p := Proxy{ID: "static", Host: host, Port: port}
		if u.User != nil {
			p.Username = u.User.Username()
			p.Password, _ = u.User.Password()
		}
		return p, nil
	}
	// host:port or user:pass@host:port
	if at := strings.LastIndex(raw, "@"); at > 0 {
		userInfo := raw[:at]
		hostPort := raw[at+1:]
		p, err := parseHostPort(hostPort)
		if err != nil {
			return Proxy{}, err
		}
		p.ID = "static"
		if colon := strings.Index(userInfo, ":"); colon > 0 {
			p.Username = userInfo[:colon]
			p.Password = userInfo[colon+1:]
		} else {
			p.Username = userInfo
		}
		return p, nil
	}
	p, err := parseHostPort(raw)
	if err != nil {
		return Proxy{}, err
	}
	p.ID = "static"
	return p, nil
}

func parseHostPort(raw string) (Proxy, error) {
	host, portStr, ok := strings.Cut(raw, ":")
	if !ok || strings.TrimSpace(host) == "" {
		return Proxy{}, fmt.Errorf("proxy: static proxy must be host:port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return Proxy{}, fmt.Errorf("proxy: invalid static proxy port")
	}
	return Proxy{Host: host, Port: port}, nil
}
