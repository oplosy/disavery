package lab_test

import (
	"context"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/oplosy/disavery/internal/lab"
)

// fakeDNS answers A queries from records with the given TTL and counts queries.
type fakeDNS struct {
	conn    net.PacketConn
	queries atomic.Int32
	mu      sync.Mutex
	records map[string][4]byte
	ttl     uint32
}

func startDNS(t *testing.T, ttl uint32, records map[string][4]byte) *fakeDNS {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDNS{conn: conn, records: records, ttl: ttl}
	t.Cleanup(func() { conn.Close() })
	go d.serve()
	return d
}

func (d *fakeDNS) serve() {
	buf := make([]byte, 512)
	for {
		n, addr, err := d.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		d.queries.Add(1)
		var q dnsmessage.Message
		if q.Unpack(buf[:n]) != nil || len(q.Questions) != 1 {
			continue
		}
		resp := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true}, Questions: q.Questions}
		d.mu.Lock()
		ip, ok := d.records[q.Questions[0].Name.String()]
		d.mu.Unlock()
		if ok {
			resp.Answers = []dnsmessage.Resource{{
				Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: d.ttl},
				Body:   &dnsmessage.AResource{A: ip},
			}}
		} else {
			resp.RCode = dnsmessage.RCodeNameError
		}
		packed, _ := resp.Pack()
		_, _ = d.conn.WriteTo(packed, addr)
	}
}

func (d *fakeDNS) set(name string, ip [4]byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records[name] = ip
}

func TestResolverHonoursTTL(t *testing.T) {
	dns := startDNS(t, 30, map[string][4]byte{"docs.disavery.test.": {172, 31, 0, 11}})
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	r := &lab.Resolver{Server: dns.conn.LocalAddr().String(), Timeout: time.Second, Now: func() time.Time { return now }}
	ctx := context.Background()

	lookup := func() string {
		t.Helper()
		addrs, err := r.LookupA(ctx, "docs.disavery.test")
		if err != nil || len(addrs) != 1 {
			t.Fatalf("lookup: %v %v", addrs, err)
		}
		return addrs[0].String()
	}
	if got := lookup(); got != "172.31.0.11" {
		t.Fatalf("got %s", got)
	}
	dns.set("docs.disavery.test.", [4]byte{172, 31, 0, 21}) // repoint
	now = now.Add(29 * time.Second)
	if got := lookup(); got != "172.31.0.11" || dns.queries.Load() != 1 {
		t.Fatalf("within TTL: got %s after %d queries", got, dns.queries.Load())
	}
	now = now.Add(2 * time.Second)
	if got := lookup(); got != "172.31.0.21" || dns.queries.Load() != 2 {
		t.Fatalf("after TTL: got %s after %d queries", got, dns.queries.Load())
	}
	if _, err := r.LookupA(ctx, "missing.disavery.test"); err == nil {
		t.Fatal("want NXDOMAIN error")
	}
}

func TestHTTPClientUsesResolverAndCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	_, port, _ := net.SplitHostPort(u.Host)
	// httptest certificates are valid for example.com.
	dns := startDNS(t, 30, map[string][4]byte{"example.com.": {127, 0, 0, 1}})
	r := &lab.Resolver{Server: dns.conn.LocalAddr().String(), Timeout: time.Second, Now: time.Now}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	c, err := lab.NewHTTPClient(r, ca, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get("https://example.com:" + port + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if _, err := lab.NewHTTPClient(r, []byte("not pem"), time.Second); err == nil {
		t.Fatal("want error for a CA without certificates")
	}
}

func TestInventory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yml")
	content := `"all":
  "children":
    "db":
      "hosts":
        "db-a": {"site": "a", "wan_ip": "172.31.0.22"}
    "site_a":
      "hosts":
        "db-a": {}
        "app-a": {}
    "site_b":
      "hosts": {}
    "vaults":
      "hosts":
        "vault": {"wan_ip": "172.31.0.40"}
  "vars":
    "active_site": "a"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	inv, err := lab.LoadInventory(path)
	if err != nil {
		t.Fatal(err)
	}
	if inv.DBHost() != "db-a" || !slices.Equal(inv.HostNames(), []string{"app-a", "db-a", "vault"}) {
		t.Fatalf("inventory %+v", inv)
	}
	if !slices.Equal(inv.Groups["site_a"], []string{"app-a", "db-a"}) || inv.Hosts["db-a"]["wan_ip"] != "172.31.0.22" {
		t.Fatalf("groups %v hosts %v", inv.Groups, inv.Hosts)
	}
}

func TestSecretsString(t *testing.T) {
	s := lab.Secrets{"minio": map[string]any{"user": "u"}}
	if v, err := s.String("minio", "user"); err != nil || v != "u" {
		t.Fatalf("%q %v", v, err)
	}
	for _, path := range [][]string{{"minio", "nope"}, {"minio", "user", "deeper"}, {"x"}} {
		if _, err := s.String(path...); err == nil {
			t.Fatalf("%v: want error", path)
		}
	}
}
