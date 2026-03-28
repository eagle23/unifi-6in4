package ra

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/mdlayher/ndp"
)

// BuildRA creates a Router Advertisement message with the given prefix and DNS servers.
func BuildRA(prefix netip.Prefix, dnsServers []netip.Addr) *ndp.RouterAdvertisement {
	ra := &ndp.RouterAdvertisement{
		CurrentHopLimit:           64,
		ManagedConfiguration:      false,
		OtherConfiguration:        false,
		RouterSelectionPreference: ndp.Medium,
		RouterLifetime:            30 * time.Minute,
		Options: []ndp.Option{
			&ndp.PrefixInformation{
				PrefixLength:                   uint8(prefix.Bits()),
				OnLink:                         true,
				AutonomousAddressConfiguration: true,
				ValidLifetime:                  2 * time.Hour,
				PreferredLifetime:              30 * time.Minute,
				Prefix:                         prefix.Addr(),
			},
		},
	}
	if len(dnsServers) > 0 {
		ra.Options = append(ra.Options, &ndp.RecursiveDNSServer{
			Lifetime: 30 * time.Minute,
			Servers:  dnsServers,
		})
	}
	return ra
}

// NetworkEntry describes a network interface and its IPv6 prefix.
type NetworkEntry struct {
	Interface string
	Prefix    netip.Prefix
}

// AdvertiserConfig holds configuration for the RA advertiser.
type AdvertiserConfig struct {
	Networks []NetworkEntry
	DNS      []netip.Addr
	Interval time.Duration
}

type connEntry struct {
	conn   *ndp.Conn
	iface  *net.Interface
	prefix netip.Prefix
}

// Advertiser sends periodic Router Advertisements on one or more interfaces.
type Advertiser struct {
	conns  []connEntry
	dns    []netip.Addr
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewAdvertiser creates and starts an Advertiser for the given config.
func NewAdvertiser(cfg AdvertiserConfig) (*Advertiser, error) {
	var conns []connEntry
	for _, nw := range cfg.Networks {
		iface, err := net.InterfaceByName(nw.Interface)
		if err != nil {
			closeConns(conns)
			return nil, fmt.Errorf("interface %q: %w", nw.Interface, err)
		}
		conn, _, err := ndp.Listen(iface, ndp.LinkLocal)
		if err != nil {
			closeConns(conns)
			return nil, fmt.Errorf("ndp listen on %q: %w", nw.Interface, err)
		}
		conns = append(conns, connEntry{conn: conn, iface: iface, prefix: nw.Prefix})
	}
	ctx, cancel := context.WithCancel(context.Background())
	adv := &Advertiser{
		conns:  conns,
		dns:    cfg.DNS,
		cancel: cancel,
	}
	interval := cfg.Interval
	if interval == 0 {
		interval = 10 * time.Second
	}
	for _, ce := range conns {
		adv.wg.Add(1)
		go adv.loop(ctx, ce, interval)
	}
	return adv, nil
}

func (a *Advertiser) loop(ctx context.Context, ce connEntry, interval time.Duration) {
	defer a.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	allNodes := netip.MustParseAddr("ff02::1")
	for {
		msg := BuildRA(ce.prefix, a.dns)
		if err := ce.conn.WriteTo(msg, nil, allNodes); err != nil {
			log.Printf("ra: send on %s: %v", ce.iface.Name, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Stop cancels all advertisement goroutines and closes all NDP connections.
func (a *Advertiser) Stop() {
	a.cancel()
	a.wg.Wait()
	closeConns(a.conns)
}

func closeConns(conns []connEntry) {
	for _, c := range conns {
		c.conn.Close()
	}
}
