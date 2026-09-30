package geoip

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/stretchr/testify/require"
)

type fakeDatabase struct {
	lookups int
	closed  bool
	addr    net.IP
	code    string
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (db *fakeDatabase) Lookup(ip net.IP, result any) error {
	db.lookups++
	db.addr = ip
	record := result.(*countryRecord)
	record.Country.ISOCode = db.code
	return nil
}

func (db *fakeDatabase) Close() error {
	db.closed = true
	return nil
}

func TestLookupCountryOnlyUsesPublicAddresses(t *testing.T) {
	db := &fakeDatabase{code: "de"}
	reader := &Reader{cfg: types.GeoIPConfig{Enabled: true}, db: db}

	for _, input := range []string{
		"10.0.0.1",
		"100.64.0.1",
		"127.0.0.1",
		"169.254.0.1",
		"192.0.2.1",
		"198.18.0.1",
		"2001:db8::1",
		"::1",
		"fc00::1",
		"fe80::1",
	} {
		country, ok := reader.LookupCountry(netip.MustParseAddr(input))
		require.False(t, ok, "address %s must not be looked up (got %q)", input, country)
	}
	require.Zero(t, db.lookups)

	country, ok := reader.LookupCountry(netip.MustParseAddr("::ffff:8.8.8.8"))
	require.True(t, ok)
	require.Equal(t, "DE", country)
	require.True(t, net.ParseIP("8.8.8.8").Equal(db.addr))
	require.Equal(t, 1, db.lookups)
}

func TestLookupCountryDisabled(t *testing.T) {
	db := &fakeDatabase{code: "DE"}
	reader := &Reader{cfg: types.GeoIPConfig{}, db: db}

	country, ok := reader.LookupCountry(netip.MustParseAddr("8.8.8.8"))
	require.False(t, ok)
	require.Empty(t, country)
	require.Zero(t, db.lookups)
}

func TestOpenWithoutDatabaseKeepsGeoIPAvailableAsFallback(t *testing.T) {
	reader, err := Open(types.GeoIPConfig{
		Enabled:      true,
		DatabasePath: filepath.Join(t.TempDir(), "missing.mmdb"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })

	country, ok := reader.LookupCountry(netip.MustParseAddr("8.8.8.8"))
	require.False(t, ok)
	require.Empty(t, country)
}

func TestNextGeoIPRetryInterval(t *testing.T) {
	for _, tt := range []struct {
		current time.Duration
		want    time.Duration
	}{
		{current: 0, want: initialGeoIPRetryInterval},
		{current: initialGeoIPRetryInterval, want: 10 * time.Minute},
		{current: 3 * time.Hour, want: maxGeoIPRetryInterval},
		{current: maxGeoIPRetryInterval, want: maxGeoIPRetryInterval},
	} {
		require.Equal(t, tt.want, nextGeoIPRetryInterval(tt.current))
	}
}

func TestFailedUpdateKeepsLastGoodDatabase(t *testing.T) {
	db := &fakeDatabase{code: "FR"}
	reader := &Reader{
		cfg: types.GeoIPConfig{
			Enabled:      true,
			DatabasePath: filepath.Join(t.TempDir(), "country.mmdb"),
			SourceURL:    "https://geo.example/country.mmdb",
		},
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("not an MMDB")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})},
		db: db,
	}

	require.Error(t, reader.update(context.Background()))
	country, ok := reader.LookupCountry(netip.MustParseAddr("1.1.1.1"))
	require.True(t, ok)
	require.Equal(t, "FR", country)
	require.False(t, db.closed)
}
