package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	statusStarting = "Starting"
	statusStable   = "Stable"
	statusDupe     = "Stable (Dupe)"
	statusRotated  = "Rotated"
	statusDead     = "DEAD"
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

type ipResponse struct {
	IP     string `json:"ip"`
	City   string `json:"city"`
	Region string `json:"region"`
}

type checkResult struct {
	ip        string
	location  string
	latency   time.Duration
	bandwidth int64
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

func createTLSClient(proxy Proxy) (tls_client.HttpClient, error) {
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(tlsProfile),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithProxyUrl(proxy.ProxyURL()),
		tls_client.WithBandwidthTracker(),
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("create TLS client: %w", err)
	}

	return client, nil
}

func checkProxyIP(client tls_client.HttpClient) (string, string, error) {
	req, err := fhttp.NewRequest("GET", "https://ipinfo.io/json", nil)
	if err != nil {
		return "", "", fmt.Errorf("create request: %w", err)
	}

	secChUa := fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", "Not_A Brand";v="99"`, chromeVersion, chromeVersion)
	userAgent := fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36", chromeFullVersion)

	req.Header = fhttp.Header{
		"accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"},
		"accept-language":           {"en-US,en;q=0.9"},
		"accept-encoding":           {"gzip, deflate, br, zstd"},
		"priority":                  {"u=0, i"},
		"sec-ch-ua":                 {secChUa},
		"sec-ch-ua-mobile":          {"?0"},
		"sec-ch-ua-platform":        {`"Windows"`},
		"sec-fetch-dest":            {"document"},
		"sec-fetch-mode":            {"navigate"},
		"sec-fetch-site":            {"none"},
		"sec-fetch-user":            {"?1"},
		"upgrade-insecure-requests": {"1"},
		"user-agent":                {userAgent},
		fhttp.HeaderOrderKey: {
			"accept",
			"accept-language",
			"accept-encoding",
			"priority",
			"sec-ch-ua",
			"sec-ch-ua-mobile",
			"sec-ch-ua-platform",
			"sec-fetch-dest",
			"sec-fetch-mode",
			"sec-fetch-site",
			"sec-fetch-user",
			"upgrade-insecure-requests",
			"user-agent",
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("status code %d", resp.StatusCode)
	}

	var response ipResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", "", fmt.Errorf("decode response: %w", err)
	}

	location := fmt.Sprintf("%s, %s", response.City, response.Region)
	return response.IP, location, nil
}

func (ps *ProxySession) updateTable() {
	ps.mu.Lock()
	sessionID := ps.Proxy.ShortID()
	provider := ps.Proxy.IP
	currentIP := ps.CurrentIP
	location := ps.Location
	status := ps.Status
	checkCount := ps.CheckCount
	rotationCount := ps.RotationCount
	totalBandwidth := ps.TotalBandwidth
	firstSeen := ps.FirstSeen
	totalLatency := ps.TotalLatency
	row := ps.row
	ps.mu.Unlock()

	if globalIPTracker.isDuplicate(currentIP, sessionID) && status == statusStable {
		status = statusDupe
	}

	sessionDuration := "-"
	if !firstSeen.IsZero() {
		sessionDuration = time.Since(firstSeen).Round(time.Second).String()
	}

	bandwidthText := formatBytes(uint64(totalBandwidth))

	latencyText := "-"
	latencyColor := tcell.ColorWhite
	if checkCount > 0 && totalLatency > 0 {
		avgLatency := totalLatency / time.Duration(checkCount)
		ms := avgLatency.Milliseconds()
		latencyText = fmt.Sprintf("%d", ms)

		switch {
		case ms < 1000:
			latencyColor = tcell.ColorGreen
		case ms <= 2000:
			latencyColor = tcell.ColorYellow
		default:
			latencyColor = tcell.ColorRed
		}
	}

	app.QueueUpdateDraw(func() {
		table.GetCell(row, 0).SetText(provider)
		table.GetCell(row, 1).SetText(sessionID)
		table.GetCell(row, 2).SetText(status)
		table.GetCell(row, 3).SetText(currentIP)
		table.GetCell(row, 4).SetText(location)
		table.GetCell(row, 5).SetText(sessionDuration)
		table.GetCell(row, 6).SetText(fmt.Sprintf("%d", checkCount))
		table.GetCell(row, 7).SetText(fmt.Sprintf("%d", rotationCount))
		table.GetCell(row, 8).SetText(latencyText).SetTextColor(latencyColor)
		table.GetCell(row, 9).SetText(bandwidthText)

		var color tcell.Color
		switch status {
		case statusStable:
			color = tcell.ColorGreen
		case statusDupe:
			color = tcell.ColorYellow
		case statusRotated:
			color = tcell.ColorRed
		case statusStarting:
			color = tcell.ColorBlue
		case statusDead:
			color = tcell.ColorGray
		default:
			color = tcell.ColorWhite
		}
		table.GetCell(row, 2).SetTextColor(color)

		rotationColor := tcell.ColorWhite
		if rotationCount > 0 {
			rotationColor = tcell.ColorRed
		}
		table.GetCell(row, 7).SetTextColor(rotationColor)
	})
}

func (ps *ProxySession) performCheck() (*checkResult, error) {
	client, err := createTLSClient(ps.Proxy)
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	startTime := time.Now()
	ip, location, err := checkProxyIP(client)
	if err != nil {
		return nil, fmt.Errorf("check IP: %w", err)
	}

	result := &checkResult{
		ip:       ip,
		location: location,
		latency:  time.Since(startTime),
	}

	if tracker := client.GetBandwidthTracker(); tracker != nil {
		result.bandwidth = tracker.GetTotalBandwidth()
	}

	return result, nil
}

func (ps *ProxySession) tryCheckWithRetry(maxRetries int) *checkResult {
	for i := range maxRetries {
		result, err := ps.performCheck()
		if err == nil {
			return result
		}
		if i < maxRetries-1 {
			time.Sleep(time.Second)
		}
	}
	return nil
}

func (ps *ProxySession) updateStatus(result *checkResult) {
	sessionID := ps.Proxy.ShortID()

	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.TotalBandwidth += result.bandwidth
	ps.CheckCount++
	ps.TotalLatency += result.latency
	ps.Location = result.location

	if ps.CurrentIP == "" {
		ps.CurrentIP = result.ip
		ps.InitialIP = result.ip
		ps.FirstSeen = time.Now()
		ps.Status = statusStable
		globalIPTracker.registerIP(result.ip, sessionID)
		return
	}

	if result.ip != ps.CurrentIP {
		ps.RotationCount++
		ps.Status = statusRotated
		ps.CurrentIP = result.ip
		globalIPTracker.registerIP(result.ip, sessionID)
		return
	}

	ps.Status = statusStable
}

func (ps *ProxySession) markDead() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.Status = statusDead
}

func (ps *ProxySession) check() {
	result := ps.tryCheckWithRetry(3)
	if result == nil {
		ps.markDead()
		ps.updateTable()
		return
	}

	ps.updateStatus(result)
	ps.updateTable()
}

func monitorProxy(interval time.Duration, session *ProxySession) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	session.check()

	for range ticker.C {
		session.check()
	}
}

func updateDurations(sessions []*ProxySession) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		app.QueueUpdateDraw(func() {
			for _, s := range sessions {
				s.mu.Lock()
				seen := s.FirstSeen
				row := s.row
				s.mu.Unlock()

				if !seen.IsZero() {
					dur := time.Since(seen).Round(time.Second).String()
					table.GetCell(row, 5).SetText(dur)
				}
			}
		})
	}
}

func startMonitoring(sessions []*ProxySession, interval time.Duration, stagger time.Duration) {
	time.Sleep(100 * time.Millisecond)

	for _, session := range sessions {
		go monitorProxy(interval, session)
		time.Sleep(stagger)
	}
}

func initializeTable(proxies []Proxy) []*ProxySession {
	headers := []string{"Provider", "Session ID", "Status", "IP Address", "Location", "Duration", "Checks", "Rotations", "Avg Latency (ms)", "Bandwidth"}
	for col, header := range headers {
		cell := tview.NewTableCell(header).
			SetTextColor(tcell.ColorYellow).
			SetAlign(tview.AlignLeft).
			SetSelectable(false).
			SetExpansion(1)
		table.SetCell(0, col, cell)
	}

	sessions := make([]*ProxySession, len(proxies))
	for i, proxy := range proxies {
		session := &ProxySession{
			Proxy:  proxy,
			row:    i + 1,
			Status: statusStarting,
		}
		sessions[i] = session

		table.SetCell(i+1, 0, tview.NewTableCell(proxy.IP).SetExpansion(1))
		table.SetCell(i+1, 1, tview.NewTableCell(proxy.ShortID()).SetExpansion(1))
		table.SetCell(i+1, 2, tview.NewTableCell(statusStarting).SetTextColor(tcell.ColorWhite).SetExpansion(1))
		for col := 3; col < 10; col++ {
			text := "-"
			if col == 6 || col == 7 {
				text = "0"
			}
			table.SetCell(i+1, col, tview.NewTableCell(text).SetExpansion(1))
		}
	}

	return sessions
}

func main() {
	intervalSec := flag.Int("interval", 20, "check interval in seconds")
	staggerMs := flag.Int("stagger", 50, "stagger delay in milliseconds")
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
	table = tview.NewTable().SetBorders(false).SetFixed(1, 0)

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
