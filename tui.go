package main

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (ps *ProxySession) updateTable() {
	ps.mu.Lock()
	sessionID := ps.Proxy.ShortID()
	host := ps.Proxy.IP
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
		table.GetCell(row, 0).SetText(host)
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

func initializeTable(proxies []Proxy) []*ProxySession {
	headers := []string{"Host", "Session ID", "Status", "IP Address", "Location", "Duration", "Checks", "Rotations", "Avg Latency (ms)", "Bandwidth"}
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
