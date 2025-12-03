package main

import (
	"fmt"
	"time"

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

func (ps *ProxySession) updateTable() {
	ps.mu.Lock()
	sessionID, host, currentIP, location, status := ps.Proxy.ShortID(), ps.Proxy.IP, ps.CurrentIP, ps.Location, ps.Status
	checkCount, rotationCount, totalBandwidth, firstSeen, totalLatency, row := ps.CheckCount, ps.RotationCount, ps.TotalBandwidth, ps.FirstSeen, ps.TotalLatency, ps.row
	ps.mu.Unlock()

	if globalIPTracker.isDuplicate(currentIP, sessionID) && status == statusStable {
		status = statusDupe
	}

	duration := "-"
	if !firstSeen.IsZero() {
		duration = time.Since(firstSeen).Round(time.Second).String()
	}

	latency, latencyColor := "-", tcell.ColorWhite
	if checkCount > 0 && totalLatency > 0 {
		ms := (totalLatency / time.Duration(checkCount)).Milliseconds()
		latency = fmt.Sprintf("%d", ms)
		if ms < 1000 {
			latencyColor = tcell.ColorGreen
		} else if ms <= 2000 {
			latencyColor = tcell.ColorYellow
		} else {
			latencyColor = tcell.ColorRed
		}
	}

	statusColors := map[string]tcell.Color{
		statusStable: tcell.ColorGreen, statusDupe: tcell.ColorYellow, statusRotated: tcell.ColorRed,
		statusStarting: tcell.ColorBlue, statusDead: tcell.ColorGray,
	}

	app.QueueUpdateDraw(func() {
		table.GetCell(row, 0).SetText(host)
		table.GetCell(row, 1).SetText(sessionID)
		table.GetCell(row, 2).SetText(status).SetTextColor(statusColors[status])
		table.GetCell(row, 3).SetText(currentIP)
		table.GetCell(row, 4).SetText(location)
		table.GetCell(row, 5).SetText(duration)
		table.GetCell(row, 6).SetText(fmt.Sprintf("%d", checkCount))
		table.GetCell(row, 7).SetText(fmt.Sprintf("%d", rotationCount)).SetTextColor(map[bool]tcell.Color{true: tcell.ColorRed, false: tcell.ColorWhite}[rotationCount > 0])
		table.GetCell(row, 8).SetText(latency).SetTextColor(latencyColor)
		table.GetCell(row, 9).SetText(formatBytes(uint64(totalBandwidth)))
	})
}

func updateDurations(sessions []*ProxySession) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		app.QueueUpdateDraw(func() {
			for _, s := range sessions {
				s.mu.Lock()
				seen, row := s.FirstSeen, s.row
				s.mu.Unlock()
				if !seen.IsZero() {
					table.GetCell(row, 5).SetText(time.Since(seen).Round(time.Second).String())
				}
			}
		})
	}
}

func initializeTable(proxies []Proxy) []*ProxySession {
	for col, header := range []string{"Host", "Session ID", "Status", "IP Address", "Location", "Duration", "Checks", "Rotations", "Avg Latency (ms)", "Bandwidth"} {
		table.SetCell(0, col, tview.NewTableCell(header).SetTextColor(tcell.ColorYellow).SetAlign(tview.AlignLeft).SetSelectable(false).SetExpansion(1))
	}

	sessions := make([]*ProxySession, len(proxies))
	for i, proxy := range proxies {
		sessions[i] = &ProxySession{Proxy: proxy, row: i + 1, Status: statusStarting}
		table.SetCell(i+1, 0, tview.NewTableCell(proxy.IP).SetExpansion(1))
		table.SetCell(i+1, 1, tview.NewTableCell(proxy.ShortID()).SetExpansion(1))
		table.SetCell(i+1, 2, tview.NewTableCell(statusStarting).SetTextColor(tcell.ColorWhite).SetExpansion(1))
		for col := 3; col < 10; col++ {
			table.SetCell(i+1, col, tview.NewTableCell(map[bool]string{true: "0", false: "-"}[col == 6 || col == 7]).SetExpansion(1))
		}
	}
	return sessions
}
