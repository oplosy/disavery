// Package lab is how the CLI reaches the lab from the toolbox: DNS through the
// lab's CoreDNS, HTTPS trusting the lab CA, SSH to nodes, secrets and inventory.
package lab

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Resolver is a caching stub resolver that honours record TTLs, like a real
// client's resolver, so DNS caching delay after a repoint is part of every
// measurement (spec §6). It only resolves A records.
type Resolver struct {
	Server  string // host:port of the lab DNS, e.g. 172.31.0.10:53
	Timeout time.Duration
	Now     func() time.Time

	mu    sync.Mutex
	cache map[string]cachedA
}

type cachedA struct {
	addrs   []netip.Addr
	expires time.Time
}

// LookupA returns the IPv4 addresses of host, from cache while the TTL lasts.
func (r *Resolver) LookupA(ctx context.Context, host string) ([]netip.Addr, error) {
	name := strings.TrimSuffix(host, ".") + "."
	now := r.Now()
	r.mu.Lock()
	if c, ok := r.cache[name]; ok && now.Before(c.expires) {
		r.mu.Unlock()
		return c.addrs, nil
	}
	r.mu.Unlock()

	addrs, ttl, err := r.query(ctx, name)
	if err != nil {
		return nil, err
	}
	if ttl > 0 {
		r.mu.Lock()
		if r.cache == nil {
			r.cache = map[string]cachedA{}
		}
		r.cache[name] = cachedA{addrs: addrs, expires: now.Add(ttl)}
		r.mu.Unlock()
	}
	return addrs, nil
}

func (r *Resolver) query(ctx context.Context, name string) ([]netip.Addr, time.Duration, error) {
	qname, err := dnsmessage.NewName(name)
	if err != nil {
		return nil, 0, err
	}
	id := uint16(rand.Uint32())
	q := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: qname, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}
	packed, err := q.Pack()
	if err != nil {
		return nil, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", r.Server)
	if err != nil {
		return nil, 0, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write(packed); err != nil {
		return nil, 0, err
	}
	buf := make([]byte, 1232)
	var resp dnsmessage.Message
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, 0, fmt.Errorf("resolve %s: %w", name, err)
		}
		if err := resp.Unpack(buf[:n]); err == nil && resp.ID == id {
			break
		}
	}
	if resp.RCode != dnsmessage.RCodeSuccess {
		return nil, 0, fmt.Errorf("resolve %s: %s", name, resp.RCode)
	}
	var addrs []netip.Addr
	ttl := uint32(math.MaxUint32)
	for _, a := range resp.Answers {
		if body, ok := a.Body.(*dnsmessage.AResource); ok {
			addrs = append(addrs, netip.AddrFrom4(body.A))
			ttl = min(ttl, a.Header.TTL)
		}
	}
	if len(addrs) == 0 {
		return nil, 0, fmt.Errorf("resolve %s: no A records", name)
	}
	return addrs, time.Duration(ttl) * time.Second, nil
}
