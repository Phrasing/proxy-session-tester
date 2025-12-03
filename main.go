package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var (
	tlsProfile        = profiles.Chrome_133_PSK
	chromeVersion     = tlsProfile.GetClientHelloId().Version
	chromeFullVersion = chromeVersion + ".0.0.0"

	globalIPTracker = &IPTracker{ipToIDs: make(map[string][]string)}
	app             *tview.Application
	table           *tview.Table
)

type Proxy struct {
	IP       string
	Port     string
	Username string
	Password string
}

type ProxySession struct {
	Proxy          Proxy
	CurrentIP      string
	Location       string
	InitialIP      string
	CheckCount     int
	RotationCount  int
	FirstSeen      time.Time
	TotalLatency   time.Duration
	TotalBandwidth int64
	Status         string
	row            int
	mu             sync.Mutex
}

type IPTracker struct {
	mu      sync.RWMutex
	ipToIDs map[string][]string
}

func (t *IPTracker) registerIP(ip, sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for existingIP, ids := range t.ipToIDs {
		for i, id := range ids {
			if id == sessionID {
				t.ipToIDs[existingIP] = append(ids[:i], ids[i+1:]...)
				if len(t.ipToIDs[existingIP]) == 0 {
					delete(t.ipToIDs, existingIP)
				}
				break
			}
		}
	}

	t.ipToIDs[ip] = append(t.ipToIDs[ip], sessionID)
}

func (t *IPTracker) isDuplicate(ip, sessionID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	ids, exists := t.ipToIDs[ip]
	if !exists {
		return false
	}

	for _, id := range ids {
		if id != sessionID {
			return true
		}
	}
	return false
}

func parseProxies(filename string) ([]Proxy, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open proxies file: %w", err)
	}
	defer file.Close()

	var proxies []Proxy
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ":")

		switch len(parts) {
		case 2:
			proxies = append(proxies, Proxy{
				IP:   parts[0],
				Port: parts[1],
			})
		case 4:
			proxies = append(proxies, Proxy{
				IP:       parts[0],
				Port:     parts[1],
				Username: parts[2],
				Password: parts[3],
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read proxies file: %w", err)
	}

	return proxies, nil
}

func main() {
	intervalSec := flag.Int("interval", 20, "check interval in seconds")
	staggerMs := flag.Int("stagger", 5, "stagger delay in milliseconds")
	proxiesFile := flag.String("proxies", "proxies.txt", "path to proxies file")
	flag.Parse()

	proxies, err := parseProxies(*proxiesFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(proxies) == 0 {
		fmt.Fprintln(os.Stderr, "error: no valid proxies found")
		os.Exit(1)
	}

	app = tview.NewApplication()
	table = tview.NewTable().
		SetBorders(false).
		SetFixed(1, 0).
		SetSelectable(true, false).
		SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite))

	sessions := initializeTable(proxies)

	info := tview.NewTextView().
		SetText(fmt.Sprintf("Monitoring %d proxies (interval: %ds) | Press Ctrl+C to quit", len(proxies), *intervalSec)).
		SetTextAlign(tview.AlignCenter)

	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(info, 1, 0, false).
		AddItem(table, 0, 1, true)

	app.SetRoot(flex, true).EnableMouse(true)

	interval := time.Duration(*intervalSec) * time.Second
	stagger := time.Duration(*staggerMs) * time.Millisecond

	go startMonitoring(sessions, interval, stagger)
	go updateDurations(sessions)

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
