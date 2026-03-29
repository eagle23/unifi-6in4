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
func BuildRA(iface *net.Interface, prefix netip.Prefix, dnsServers []netip.Addr, tunnelMTU int) *ndp.RouterAdvertisement {
	options := make([]ndp.Option, 0, 4)
	if iface != nil && len(iface.HardwareAddr) > 0 {
		options = append(options, &ndp.LinkLayerAddress{
			Direction: ndp.Source,
			Addr:      append(net.HardwareAddr(nil), iface.HardwareAddr...),
		})
	}
	// Advertise tunnel MTU, not LAN interface MTU — clients must know the
	// effective path MTU through the 6in4 tunnel (e.g. 1472 for PPPoE).
	if tunnelMTU > 0 {
		options = append(options, ndp.NewMTU(uint32(tunnelMTU)))
	} else if iface != nil && iface.MTU > 0 {
		options = append(options, ndp.NewMTU(uint32(iface.MTU)))
	}
	options = append(options, &ndp.PrefixInformation{
		PrefixLength:                   uint8(prefix.Bits()),
		OnLink:                         true,
		AutonomousAddressConfiguration: true,
		ValidLifetime:                  2 * time.Hour,
		PreferredLifetime:              30 * time.Minute,
		Prefix:                         prefix.Addr(),
	})
	ra := &ndp.RouterAdvertisement{
		CurrentHopLimit:           64,
		ManagedConfiguration:      false,
		OtherConfiguration:        false,
		RouterSelectionPreference: ndp.Medium,
		RouterLifetime:            30 * time.Minute,
		Options:                   options,
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
	Networks  []NetworkEntry
	DNS       []netip.Addr
	Interval  time.Duration
	TunnelMTU int // Advertised MTU in RA; 0 = use LAN iface MTU
}

type connEntry struct {
	conn   *ndp.Conn
	iface  *net.Interface
	prefix netip.Prefix
}

// Advertiser sends periodic Router Advertisements on one or more interfaces.
type Advertiser struct {
	conns     []connEntry
	dns       []netip.Addr
	tunnelMTU int
	cancel    context.CancelFunc
	wg        sync.WaitGroup
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
		conns:     conns,
		dns:       cfg.DNS,
		tunnelMTU: cfg.TunnelMTU,
		cancel:    cancel,
	}
	interval := cfg.Interval
	if interval == 0 {
		interval = 10 * time.Second
	}
	for _, ce := range conns {
		adv.wg.Add(1)
		go adv.sendLoop(ctx, ce, interval)
		adv.wg.Add(1)
		go adv.listenLoop(ctx, ce)
	}
	return adv, nil
}

func (a *Advertiser) sendLoop(ctx context.Context, ce connEntry, interval time.Duration) {
	defer a.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	allNodes := netip.MustParseAddr("ff02::1")
	msg := BuildRA(ce.iface, ce.prefix, a.dns, a.tunnelMTU)
	for {
		if err := ce.conn.WriteTo(msg, nil, allNodes); err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			log.Printf("ra: send on %s: %v", ce.iface.Name, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Advertiser) listenLoop(ctx context.Context, ce connEntry) {
	defer a.wg.Done()
	allNodes := netip.MustParseAddr("ff02::1")
	for {
		message, _, source, err := ce.conn.ReadFrom()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			log.Printf("ra: read on %s: %v", ce.iface.Name, err)
			continue
		}
		if _, ok := message.(*ndp.RouterSolicitation); !ok {
			continue
		}
		destination := source
		if !destination.IsValid() || destination.IsUnspecified() {
			destination = allNodes
		}
		if err := ce.conn.WriteTo(BuildRA(ce.iface, ce.prefix, a.dns, a.tunnelMTU), nil, destination); err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			log.Printf("ra: respond on %s: %v", ce.iface.Name, err)
		}
	}
}

// Stop cancels all advertisement goroutines and closes all NDP connections.
func (a *Advertiser) Stop() {
	a.cancel()
	closeConns(a.conns)
	a.wg.Wait()
}

func closeConns(conns []connEntry) {
	for _, c := range conns {
		c.conn.Close()
	}
}
