// Package geoip provides local country lookups backed by a MaxMind MMDB.
package geoip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/oschwald/maxminddb-golang"
	"github.com/rs/zerolog/log"
)

const maxDatabaseSize = 100 << 20

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
}

type countryRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

type database interface {
	Lookup(net.IP, any) error
	Close() error
}

// Reader serves country lookups and optionally refreshes its MMDB in the background.
type Reader struct {
	cfg      types.GeoIPConfig
	client   *http.Client
	mu       sync.RWMutex
	updateMu sync.Mutex
	db       database
	cancel   context.CancelFunc
	done     chan struct{}
}

// Open loads the configured MMDB and starts periodic updates when enabled.
// If a download fails, an already-loaded database remains available.
func Open(cfg types.GeoIPConfig) (*Reader, error) {
	r := &Reader{
		cfg:    cfg,
		client: &http.Client{Timeout: 2 * time.Minute},
		done:   make(chan struct{}),
	}
	if !cfg.Enabled {
		close(r.done)
		return r, nil
	}
	if cfg.DatabasePath == "" {
		return nil, errors.New("geoip database_path is required when GeoIP is enabled")
	}
	if cfg.UpdateInterval < 0 {
		return nil, errors.New("geoip update interval must not be negative")
	}
	if cfg.SourceURL != "" {
		u, err := url.ParseRequestURI(cfg.SourceURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, errors.New("geoip download URL must be an absolute HTTP(S) URL")
		}
	}

	if db, err := maxminddb.Open(cfg.DatabasePath); err == nil {
		r.db = db
	} else {
		log.Warn().Err(err).Msg("GeoIP database is unavailable; country lookup will use fallback")
	}

	if cfg.SourceURL != "" {
		if err := r.update(context.Background()); err != nil {
			log.Warn().Err(err).Msg("GeoIP database update failed; retaining last good database")
		}
	}

	if cfg.SourceURL != "" && cfg.UpdateInterval > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		r.cancel = cancel
		r.done = make(chan struct{})
		go r.updateLoop(ctx)
	} else {
		close(r.done)
	}

	return r, nil
}

// LookupCountry returns the two-letter ISO country code for a public IP.
// The boolean is false when GeoIP is disabled, the address is not public,
// the database has no record, or the record has no country code.
func (r *Reader) LookupCountry(addr netip.Addr) (string, bool) {
	if r == nil || !r.cfg.Enabled {
		return "", false
	}
	addr = addr.Unmap()
	if !isPublic(addr) {
		return "", false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.db == nil {
		return "", false
	}

	var record countryRecord
	if err := r.db.Lookup(net.IP(addr.AsSlice()), &record); err != nil {
		return "", false
	}
	country := strings.ToUpper(record.Country.ISOCode)
	if len(country) != 2 {
		return "", false
	}

	return country, true
}

// Close stops the updater and closes the active MMDB reader.
func (r *Reader) Close() error {
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
		<-r.done
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return nil
	}
	err := r.db.Close()
	r.db = nil
	return err
}

func (r *Reader) updateLoop(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(r.cfg.UpdateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.update(ctx); err != nil {
				log.Warn().Err(err).Msg("GeoIP database update failed; retaining last good database")
			}
		}
	}
}

func (r *Reader) update(ctx context.Context) error {
	r.updateMu.Lock()
	defer r.updateMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(r.cfg.DatabasePath), 0o750); err != nil {
		return fmt.Errorf("creating GeoIP database directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.cfg.DatabasePath), ".geoip-*.mmdb")
	if err != nil {
		return fmt.Errorf("creating temporary GeoIP database: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.cfg.SourceURL, nil)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("creating GeoIP download request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("downloading GeoIP database: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_ = tmp.Close()
		return fmt.Errorf("downloading GeoIP database: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxDatabaseSize {
		_ = tmp.Close()
		return fmt.Errorf("GeoIP database exceeds %d bytes", maxDatabaseSize)
	}
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxDatabaseSize+1))
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing downloaded GeoIP database: %w", err)
	}
	if n > maxDatabaseSize {
		_ = tmp.Close()
		return fmt.Errorf("GeoIP database exceeds %d bytes", maxDatabaseSize)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing downloaded GeoIP database: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing downloaded GeoIP database: %w", err)
	}

	newDB, err := maxminddb.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("validating downloaded GeoIP database: %w", err)
	}
	if err := os.Rename(tmpPath, r.cfg.DatabasePath); err != nil {
		_ = newDB.Close()
		return fmt.Errorf("installing downloaded GeoIP database: %w", err)
	}

	r.mu.Lock()
	oldDB := r.db
	r.db = newDB
	r.mu.Unlock()
	if oldDB != nil {
		_ = oldDB.Close()
	}
	return nil
}

func isPublic(addr netip.Addr) bool {
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
