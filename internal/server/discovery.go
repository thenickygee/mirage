
package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/mdns"
)

const (
	mdnsScanInterval = 5 * time.Second
	mdnsQueryTimeout = 2 * time.Second
)

// silentLogger discards all mdns library log output so it doesn't bleed into the TUI.
var silentLogger = log.New(io.Discard, "", 0)

// Discover continuously scans for OpenCode instances via mDNS and adds them
// to the pool. It runs until ctx is cancelled.
func Discover(ctx context.Context, pool *Pool) {
	scan := func() {
		urls := findOpenCodeInstances()
		for _, u := range urls {
			// Add is a no-op if the URL is already in the pool; errors are
			// logged internally by Client so we intentionally discard them here.
			_ = pool.Add(u)
		}
	}

	scan()
	ticker := time.NewTicker(mdnsScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scan()
		}
	}
}

// findOpenCodeInstances queries mDNS for _http._tcp services whose name starts
// with "opencode-", verifies the health endpoint, and returns reachable URLs.
func findOpenCodeInstances() []string {
	entries := make(chan *mdns.ServiceEntry, 16)
	var found []string

	go func() {
		params := mdns.DefaultParams("_http._tcp")
		params.DisableIPv6 = true
		params.Timeout = mdnsQueryTimeout
		params.Entries = entries
		params.Logger = silentLogger
		mdns.Query(params) //nolint:errcheck
		close(entries)
	}()

	for entry := range entries {
		if !strings.HasPrefix(entry.Name, "opencode-") {
			continue
		}
		url := fmt.Sprintf("http://%s:%d", entry.AddrV4.String(), entry.Port)
		if isHealthy(url) {
			found = append(found, url)
		}
	}
	return found
}

// isHealthy checks that the OpenCode health endpoint responds successfully.
func isHealthy(baseURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/global/health")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == 200
}
