package main

import (
	"bufio"
	"bytes"
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// -----------------------------------------------------------------------------
// Config
// -----------------------------------------------------------------------------

var (
	listenAddr    = getEnv("LISTEN_ADDR", ":9586")
	containerName = getEnv("AWG_CONTAINER", "amnezia-awg2")
	awgBin        = getEnv("AWG_BIN", "awg")
	scrapeTimeout = 10 * time.Second
)

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// -----------------------------------------------------------------------------
// Metrics descriptors
// -----------------------------------------------------------------------------

var (
	peerRxBytes = prometheus.NewDesc(
		"awg_peer_receive_bytes_total",
		"Total bytes received from peer",
		[]string{"interface", "peer", "allowed_ips", "endpoint"},
		nil,
	)
	peerTxBytes = prometheus.NewDesc(
		"awg_peer_transmit_bytes_total",
		"Total bytes transmitted to peer",
		[]string{"interface", "peer", "allowed_ips", "endpoint"},
		nil,
	)
	peerLastHandshake = prometheus.NewDesc(
		"awg_peer_last_handshake_seconds",
		"Unix timestamp of the last handshake with peer (0 = never)",
		[]string{"interface", "peer", "allowed_ips", "endpoint"},
		nil,
	)
	peerConnected = prometheus.NewDesc(
		"awg_peer_connected",
		"1 if last handshake was less than 3 minutes ago, 0 otherwise",
		[]string{"interface", "peer", "allowed_ips", "endpoint"},
		nil,
	)
	peersTotal = prometheus.NewDesc(
		"awg_peers_total",
		"Total number of configured peers per interface",
		[]string{"interface"},
		nil,
	)
	peersOnline = prometheus.NewDesc(
		"awg_peers_online",
		"Number of peers with a recent handshake (< 3 min) per interface",
		[]string{"interface"},
		nil,
	)
	scrapeErrors = prometheus.NewDesc(
		"awg_scrape_errors_total",
		"Total number of errors when scraping awg",
		nil,
		nil,
	)
)

// -----------------------------------------------------------------------------
// Peer data
// -----------------------------------------------------------------------------

type peerInfo struct {
	iface         string
	pubKey        string
	endpoint      string
	allowedIPs    string
	lastHandshake int64
	rxBytes       int64
	txBytes       int64
}

// -----------------------------------------------------------------------------
// Collector
// -----------------------------------------------------------------------------

type awgCollector struct {
	errorCount float64
}

func (c *awgCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- peerRxBytes
	ch <- peerTxBytes
	ch <- peerLastHandshake
	ch <- peerConnected
	ch <- peersTotal
	ch <- peersOnline
	ch <- scrapeErrors
}

func (c *awgCollector) Collect(ch chan<- prometheus.Metric) {
	peers, err := collectPeers()
	if err != nil {
		log.Printf("ERROR collecting peers: %v", err)
		c.errorCount++
		ch <- prometheus.MustNewConstMetric(scrapeErrors, prometheus.CounterValue, c.errorCount)
		return
	}

	ch <- prometheus.MustNewConstMetric(scrapeErrors, prometheus.CounterValue, c.errorCount)

	type ifaceStats struct {
		total  float64
		online float64
	}
	ifaces := map[string]*ifaceStats{}

	now := time.Now().Unix()

	for _, p := range peers {
		if _, ok := ifaces[p.iface]; !ok {
			ifaces[p.iface] = &ifaceStats{}
		}
		ifaces[p.iface].total++

		connected := 0.0
		if p.lastHandshake > 0 && (now-p.lastHandshake) < 180 {
			connected = 1.0
			ifaces[p.iface].online++
		}

		labels := []string{p.iface, p.pubKey, p.allowedIPs, p.endpoint}

		ch <- prometheus.MustNewConstMetric(peerRxBytes, prometheus.CounterValue, float64(p.rxBytes), labels...)
		ch <- prometheus.MustNewConstMetric(peerTxBytes, prometheus.CounterValue, float64(p.txBytes), labels...)
		ch <- prometheus.MustNewConstMetric(peerLastHandshake, prometheus.GaugeValue, float64(p.lastHandshake), labels...)
		ch <- prometheus.MustNewConstMetric(peerConnected, prometheus.GaugeValue, connected, labels...)
	}

	for iface, stats := range ifaces {
		ch <- prometheus.MustNewConstMetric(peersTotal, prometheus.GaugeValue, stats.total, iface)
		ch <- prometheus.MustNewConstMetric(peersOnline, prometheus.GaugeValue, stats.online, iface)
	}
}

// -----------------------------------------------------------------------------
// awg show all dump parser
//
// Server line (Amnezia adds obfuscation fields after port):
//   <iface>  <pubkey>  <privkey>  <port>  [jc jmin jmax s1 s2 h1 h2 h3 h4 ...]  <fwmark>
//
// Peer line:
//   <iface>  <pubkey>  <preshared>  <endpoint>  <allowed_ips>  <last_handshake>  <rx>  <tx>  <keepalive>
//
// Heuristic: field[3] is numeric port → server line; otherwise peer line.
// -----------------------------------------------------------------------------

func collectPeers() ([]peerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx,
		"docker", "exec", containerName,
		awgBin, "show", "all", "dump",
	)

	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return nil, err
	}

	return parseDump(out.String()), nil
}

func parseDump(raw string) []peerInfo {
	var peers []peerInfo

	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}

		// Server line detection: field[3] is a valid port number
		if isPort(fields[3]) {
			continue
		}

		// Peer line needs at least 9 fields
		if len(fields) < 9 {
			continue
		}

		peers = append(peers, peerInfo{
			iface:         fields[0],
			pubKey:        fields[1],
			endpoint:      normalizeField(fields[3]),
			allowedIPs:    normalizeField(fields[4]),
			lastHandshake: parseInt64(fields[5]),
			rxBytes:       parseInt64(fields[6]),
			txBytes:       parseInt64(fields[7]),
		})
	}

	return peers
}

func isPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535
}

func normalizeField(s string) string {
	if s == "(none)" || s == "(null)" {
		return ""
	}
	return s
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// -----------------------------------------------------------------------------
// Main
// -----------------------------------------------------------------------------

func main() {
	collector := &awgCollector{}
	prometheus.MustRegister(collector)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("Starting awg-exporter on %s (container=%s)", listenAddr, containerName)
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}
