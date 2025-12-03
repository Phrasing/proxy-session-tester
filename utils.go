package main

import (
	"fmt"
	"hash/fnv"
)

func (p *Proxy) ShortID() string {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s:%s:%s:%s", p.IP, p.Port, p.Username, p.Password)
	return fmt.Sprintf("%x", h.Sum64())
}

func (p *Proxy) ProxyURL() string {
	if p.Username != "" && p.Password != "" {
		return fmt.Sprintf("http://%s:%s@%s:%s", p.Username, p.Password, p.IP, p.Port)
	}
	return fmt.Sprintf("http://%s:%s", p.IP, p.Port)
}

func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
