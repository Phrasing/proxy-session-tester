package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

var (
	tlsProfile        = profiles.Chrome_133_PSK
	chromeVersion     = tlsProfile.GetClientHelloId().Version
	chromeFullVersion = chromeVersion + ".0.0.0"
)

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

func createTLSClient(proxy Proxy) (tls_client.HttpClient, error) {
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(tlsProfile),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithProxyUrl(proxy.ProxyURL()),
		tls_client.WithBandwidthTracker(),
	)
	if err != nil {
		return nil, fmt.Errorf("create TLS client: %w", err)
	}
	return client, nil
}

func checkProxyIP(client tls_client.HttpClient) (string, string, error) {
	req, _ := fhttp.NewRequest("GET", "https://ipinfo.io/json", nil)
	req.Header = fhttp.Header{
		"accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"},
		"accept-language":           {"en-US,en;q=0.9"},
		"accept-encoding":           {"gzip, deflate, br, zstd"},
		"priority":                  {"u=0, i"},
		"sec-ch-ua":                 {fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", "Not_A Brand";v="99"`, chromeVersion, chromeVersion)},
		"sec-ch-ua-mobile":          {"?0"},
		"sec-ch-ua-platform":        {`"Windows"`},
		"sec-fetch-dest":            {"document"},
		"sec-fetch-mode":            {"navigate"},
		"sec-fetch-site":            {"none"},
		"sec-fetch-user":            {"?1"},
		"upgrade-insecure-requests": {"1"},
		"user-agent":                {fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36", chromeFullVersion)},
		fhttp.HeaderOrderKey:        {"accept", "accept-language", "accept-encoding", "priority", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "sec-fetch-dest", "sec-fetch-mode", "sec-fetch-site", "sec-fetch-user", "upgrade-insecure-requests", "user-agent"},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("status code %d", resp.StatusCode)
	}

	var r ipResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", "", fmt.Errorf("decode response: %w", err)
	}

	return r.IP, fmt.Sprintf("%s, %s", r.City, r.Region), nil
}

func (ps *ProxySession) performCheck() (*checkResult, error) {
	client, err := createTLSClient(ps.Proxy)
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	start := time.Now()
	ip, location, err := checkProxyIP(client)
	if err != nil {
		return nil, fmt.Errorf("check IP: %w", err)
	}

	result := &checkResult{ip: ip, location: location, latency: time.Since(start)}
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
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.TotalBandwidth += result.bandwidth
	ps.CheckCount++
	ps.TotalLatency += result.latency
	ps.Location = result.location

	if ps.CurrentIP == "" {
		ps.CurrentIP, ps.InitialIP, ps.FirstSeen, ps.Status = result.ip, result.ip, time.Now(), statusStable
		globalIPTracker.registerIP(result.ip, ps.Proxy.ShortID())
	} else if result.ip != ps.CurrentIP {
		ps.RotationCount++
		ps.CurrentIP, ps.Status = result.ip, statusRotated
		globalIPTracker.registerIP(result.ip, ps.Proxy.ShortID())
	} else {
		ps.Status = statusStable
	}
}

func (ps *ProxySession) check() {
	if result := ps.tryCheckWithRetry(3); result != nil {
		ps.updateStatus(result)
	} else {
		ps.mu.Lock()
		ps.Status = statusDead
		ps.mu.Unlock()
	}
	ps.updateTable()
}

func monitorProxy(interval time.Duration, session *ProxySession) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for session.check(); true; session.check() {
		<-ticker.C
	}
}

func startMonitoring(sessions []*ProxySession, interval time.Duration, stagger time.Duration) {
	time.Sleep(100 * time.Millisecond)

	for _, session := range sessions {
		go monitorProxy(interval, session)
		time.Sleep(stagger)
	}
}
