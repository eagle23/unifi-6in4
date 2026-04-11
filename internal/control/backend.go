package control

import (
	"bytes"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

const (
	iptablesChainInput   = "IPV6TUNNEL_INPUT"
	iptablesChainForward = "IPV6TUNNEL_FORWARD"
	iptablesChainMSS     = "IPV6TUNNEL_MSS"
)

var (
	ipv4AddressPattern  = regexp.MustCompile(`inet (\d+\.\d+\.\d+\.\d+)`)
	interfaceMTUPattern = regexp.MustCompile(`mtu (\d+)`)
	pingTimePattern     = regexp.MustCompile(`time=([0-9.]+)`)
	ipv6RouteSrcPattern = regexp.MustCompile(`\bsrc ([0-9a-fA-F:]+)\b`)
)

var sleepFn = time.Sleep

// Backend observes and applies the dataplane state.
type Backend interface {
	Reconcile(input ReconcileInput) error
	Observe(input ObserveInput) (*Observation, error)
	Probe(target string) (ProbeResult, error)
	RepairAddresses(input RepairInput) error
}

// ReconcileInput describes the desired dataplane state.
type ReconcileInput struct {
	Config      *config.Config
	ConfigValid bool
	Force       bool
	Applied     AppliedState
}

// RepairInput describes a lightweight reconciliation that only ensures
// managed IPv6 addresses are present, without rebuilding the tunnel.
type RepairInput struct {
	Config *config.Config
}

// ObserveInput describes the current read-only dataplane observation request.
type ObserveInput struct {
	WANInterface string
	Config       *config.Config
}

// Observation holds a lightweight dataplane observation.
type Observation struct {
	TunnelUp             bool
	WANIPv4              string
	MissingIPv6Addresses []string
}

// ProbeResult holds the result of an IPv6 health probe.
type ProbeResult struct {
	PingOK bool
	PingMs int
}

// CommandRunner abstracts command execution for tests.
type CommandRunner interface {
	CombinedOutput(name string, args ...string) ([]byte, error)
}

// ExecRunner executes commands on the local system.
type ExecRunner struct{}

// CombinedOutput runs a system command and returns its combined stdout/stderr.
func (ExecRunner) CombinedOutput(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// SystemBackend applies state using local CLI tools.
type SystemBackend struct {
	runner CommandRunner
}

// NewSystemBackend creates the real CLI-based backend.
func NewSystemBackend() *SystemBackend {
	return &SystemBackend{runner: ExecRunner{}}
}

// NewSystemBackendWithRunner creates a backend with a custom command runner.
func NewSystemBackendWithRunner(runner CommandRunner) *SystemBackend {
	return &SystemBackend{runner: runner}
}

// Reconcile converges the dataplane to the desired config.
func (b *SystemBackend) Reconcile(input ReconcileInput) error {
	if input.Config == nil {
		return fmt.Errorf("config is required")
	}
	b.teardown(input.Applied)
	if !input.ConfigValid || !input.Config.Tunnel.Enabled {
		return nil
	}
	wanIP, err := b.getWANIPv4(input.Config.Server.WANInterface)
	if err != nil {
		return err
	}
	resolvedMTU, err := b.resolveTunnelMTU(input.Config.Tunnel.MTU, input.Config.Server.WANInterface)
	if err != nil {
		return err
	}
	input.Config.Tunnel.MTU = resolvedMTU
	b.runBestEffort("modprobe", "sit")
	if err := b.run("ip", "route", "replace", input.Config.Tunnel.RemoteEndpoint+"/32", "dev", input.Config.Server.WANInterface, "src", wanIP); err != nil {
		return err
	}
	if err := b.run("ip", "tunnel", "add", tunnel.InterfaceName, "mode", "sit", "remote", input.Config.Tunnel.RemoteEndpoint, "local", wanIP, "ttl", strconv.Itoa(input.Config.Tunnel.TTL)); err != nil {
		return err
	}
	if err := b.run("ip", "link", "set", tunnel.InterfaceName, "mtu", strconv.Itoa(input.Config.Tunnel.MTU)); err != nil {
		return err
	}
	if err := b.run("ip", "link", "set", tunnel.InterfaceName, "up"); err != nil {
		return err
	}
	if err := b.ensureIPv6AddressPresent(tunnel.InterfaceName, input.Config.Tunnel.LocalIPv6); err != nil {
		return err
	}
	if err := b.run("sysctl", "-w", "net.ipv6.conf.all.forwarding=1"); err != nil {
		return err
	}
	if input.Config.LAN.Enabled {
		managedInterfaces := make(map[string]struct{}, len(input.Config.LAN.Networks))
		for _, network := range input.Config.LAN.Networks {
			if _, ok := managedInterfaces[network.Interface]; !ok {
				if err := b.ensureLANInterfaceReady(network.Interface); err != nil {
					return err
				}
				managedInterfaces[network.Interface] = struct{}{}
			}
			gatewayAddress, err := gatewayForPrefix(network.Prefix)
			if err != nil {
				return err
			}
			if err := b.ensureIPv6AddressPresent(network.Interface, gatewayAddress); err != nil {
				return err
			}
		}
	}
	defaultRouteArgs, err := buildDefaultRouteArgs(input.Config)
	if err != nil {
		return err
	}
	if err := b.run("ip", defaultRouteArgs...); err != nil {
		return err
	}
	// IPv4: allow protocol 41 (6in4) on WAN
	b.runBestEffort("iptables", "-N", iptablesChainInput)
	b.runBestEffort("iptables", "-F", iptablesChainInput)
	b.runBestEffort("iptables", "-A", iptablesChainInput, "-i", input.Config.Server.WANInterface, "-p", "41", "-j", "ACCEPT")
	b.ensureJump("iptables", "INPUT", iptablesChainInput)
	// IPv6: stateful forwarding through tunnel
	b.runBestEffort("ip6tables", "-N", iptablesChainForward)
	b.runBestEffort("ip6tables", "-F", iptablesChainForward)
	b.runBestEffort("ip6tables", "-A", iptablesChainForward, "-i", tunnel.InterfaceName, "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT")
	b.runBestEffort("ip6tables", "-A", iptablesChainForward, "-o", tunnel.InterfaceName, "-j", "ACCEPT")
	b.runBestEffort("ip6tables", "-A", iptablesChainForward, "-i", tunnel.InterfaceName, "-m", "state", "--state", "NEW", "-j", "DROP")
	b.ensureJump("ip6tables", "FORWARD", iptablesChainForward)
	// TCP MSS clamping — prevents broken sites due to PMTUD failures
	b.runBestEffort("ip6tables", "-t", "mangle", "-N", iptablesChainMSS)
	b.runBestEffort("ip6tables", "-t", "mangle", "-F", iptablesChainMSS)
	b.runBestEffort("ip6tables", "-t", "mangle", "-A", iptablesChainMSS, "-o", tunnel.InterfaceName, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu")
	b.ensureJumpTable("ip6tables", "mangle", "FORWARD", iptablesChainMSS)
	return nil
}

func (b *SystemBackend) resolveTunnelMTU(configuredMTU int, wanInterface string) (int, error) {
	if configuredMTU > 0 {
		return configuredMTU, nil
	}
	wanMTU, err := b.getInterfaceMTU(wanInterface)
	if err != nil {
		return 0, err
	}
	const tunnelOverhead = 20
	const minimumIPv6MTU = 1280
	resolvedMTU := wanMTU - tunnelOverhead
	if resolvedMTU < minimumIPv6MTU {
		return 0, fmt.Errorf("resolved tunnel MTU %d is below IPv6 minimum %d", resolvedMTU, minimumIPv6MTU)
	}
	return resolvedMTU, nil
}

func (b *SystemBackend) ensureLANInterfaceReady(interfaceName string) error {
	if err := b.run("sysctl", "-w", fmt.Sprintf("net.ipv6.conf.%s.accept_dad=0", interfaceName)); err != nil {
		return err
	}
	output, err := b.runner.CombinedOutput("ip", "-6", "addr", "show", "dev", interfaceName)
	if err != nil {
		return fmt.Errorf("read IPv6 addresses on %s: %w", interfaceName, err)
	}
	if !hasProblematicLinkLocal(string(output)) {
		return nil
	}
	// Toggle disable_ipv6 to reset IPv6 state on the interface without bouncing L2.
	// L2 stays up, so IPv4/DHCP clients are unaffected; IPv6 blackout is sub-second.
	if err := b.run("sysctl", "-w", fmt.Sprintf("net.ipv6.conf.%s.disable_ipv6=1", interfaceName)); err != nil {
		return err
	}
	sleepFn(300 * time.Millisecond)
	if err := b.run("sysctl", "-w", fmt.Sprintf("net.ipv6.conf.%s.disable_ipv6=0", interfaceName)); err != nil {
		return err
	}
	sleepFn(500 * time.Millisecond)
	return nil
}

// RepairAddresses ensures all managed IPv6 addresses are present without rebuilding
// the tunnel, touching firewall rules, or bouncing LAN interfaces.
func (b *SystemBackend) RepairAddresses(input RepairInput) error {
	if input.Config == nil || !input.Config.Tunnel.Enabled {
		return nil
	}
	if err := b.ensureIPv6AddressPresent(tunnel.InterfaceName, input.Config.Tunnel.LocalIPv6); err != nil {
		return err
	}
	if !input.Config.LAN.Enabled {
		return nil
	}
	for _, network := range input.Config.LAN.Networks {
		gatewayAddress, err := gatewayForPrefix(network.Prefix)
		if err != nil {
			return err
		}
		if err := b.ensureIPv6AddressPresent(network.Interface, gatewayAddress); err != nil {
			return err
		}
	}
	return nil
}

func (b *SystemBackend) ensureIPv6AddressPresent(interfaceName string, prefixValue string) error {
	state, err := b.queryInterfaceIPv6Address(interfaceName, prefixValue)
	if err != nil {
		return err
	}
	switch state {
	case ipv6AddressHealthy:
		return nil
	case ipv6AddressUnhealthy:
		// dadfailed/tentative: address is present but unusable — drop it first so
		// the fresh add replaces the broken entry instead of failing with EEXIST.
		if err := b.run("ip", "-6", "addr", "del", prefixValue, "dev", interfaceName); err != nil {
			return err
		}
	}
	return b.run("ip", "-6", "addr", "add", prefixValue, "dev", interfaceName)
}

// Observe reads the lightweight runtime state.
func (b *SystemBackend) Observe(input ObserveInput) (*Observation, error) {
	observation := &Observation{}
	if _, err := b.runner.CombinedOutput("ip", "link", "show", tunnel.InterfaceName); err == nil {
		observation.TunnelUp = true
	}
	if strings.TrimSpace(input.WANInterface) != "" {
		wanIP, err := b.getWANIPv4(input.WANInterface)
		if err == nil {
			observation.WANIPv4 = wanIP
		}
	}
	missingAddresses, err := b.collectMissingManagedIPv6Addresses(input.Config)
	if err != nil {
		return observation, err
	}
	observation.MissingIPv6Addresses = missingAddresses
	return observation, nil
}

// Probe runs a single IPv6 ping probe.
func (b *SystemBackend) Probe(target string) (ProbeResult, error) {
	args := []string{"-6", "-n", "-c", "1", "-W", "3"}
	sourceAddress, err := b.resolveProbeSourceAddress(target)
	if err != nil {
		return ProbeResult{}, err
	}
	if sourceAddress != "" {
		args = append(args, "-I", sourceAddress)
	}
	args = append(args, target)
	output, err := b.runner.CombinedOutput("ping", args...)
	if err != nil {
		return ProbeResult{PingOK: false, PingMs: 0}, nil
	}
	match := pingTimePattern.FindStringSubmatch(string(output))
	if len(match) < 2 {
		return ProbeResult{PingOK: true, PingMs: 0}, nil
	}
	value, parseErr := strconv.ParseFloat(match[1], 64)
	if parseErr != nil {
		return ProbeResult{PingOK: true, PingMs: 0}, nil
	}
	return ProbeResult{PingOK: true, PingMs: int(value)}, nil
}

func (b *SystemBackend) teardown(applied AppliedState) {
	b.removeJump("iptables", "INPUT", iptablesChainInput)
	b.runBestEffort("iptables", "-F", iptablesChainInput)
	b.runBestEffort("iptables", "-X", iptablesChainInput)
	b.removeJump("ip6tables", "FORWARD", iptablesChainForward)
	b.runBestEffort("ip6tables", "-F", iptablesChainForward)
	b.runBestEffort("ip6tables", "-X", iptablesChainForward)
	b.removeJumpTable("ip6tables", "mangle", "FORWARD", iptablesChainMSS)
	b.runBestEffort("ip6tables", "-t", "mangle", "-F", iptablesChainMSS)
	b.runBestEffort("ip6tables", "-t", "mangle", "-X", iptablesChainMSS)
	for _, network := range applied.Networks {
		gatewayAddress, err := gatewayForPrefix(network.Prefix)
		if err != nil {
			continue
		}
		b.runBestEffort("ip", "-6", "addr", "del", gatewayAddress, "dev", network.Interface)
	}
	if applied.RemoteEndpoint != "" && applied.WANInterface != "" {
		b.runBestEffort("ip", "route", "del", applied.RemoteEndpoint+"/32", "dev", applied.WANInterface)
	}
	b.runBestEffort("ip", "-6", "route", "del", "::/0", "dev", tunnel.InterfaceName)
	b.runBestEffort("ip", "tunnel", "del", tunnel.InterfaceName)
}

func (b *SystemBackend) getWANIPv4(interfaceName string) (string, error) {
	output, err := b.runner.CombinedOutput("ip", "-4", "addr", "show", interfaceName)
	if err != nil {
		return "", fmt.Errorf("read WAN IPv4: %w", err)
	}
	match := ipv4AddressPattern.FindStringSubmatch(string(output))
	if len(match) < 2 {
		return "", fmt.Errorf("WAN IPv4 not found on %s", interfaceName)
	}
	return match[1], nil
}

func (b *SystemBackend) getInterfaceMTU(interfaceName string) (int, error) {
	output, err := b.runner.CombinedOutput("ip", "link", "show", interfaceName)
	if err != nil {
		return 0, fmt.Errorf("read interface MTU: %w", err)
	}
	match := interfaceMTUPattern.FindStringSubmatch(string(output))
	if len(match) < 2 {
		return 0, fmt.Errorf("MTU not found on %s", interfaceName)
	}
	mtuValue, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, fmt.Errorf("parse MTU on %s: %w", interfaceName, err)
	}
	return mtuValue, nil
}

func hasProblematicLinkLocal(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != "inet6" {
			continue
		}
		prefix, err := netip.ParsePrefix(fields[1])
		if err != nil {
			continue
		}
		if !prefix.Addr().IsLinkLocalUnicast() {
			continue
		}
		for _, flag := range fields[2:] {
			if flag == "dadfailed" || flag == "tentative" {
				return true
			}
		}
	}
	return false
}

func (b *SystemBackend) collectMissingManagedIPv6Addresses(cfg *config.Config) ([]string, error) {
	if cfg == nil || !cfg.Tunnel.Enabled {
		return []string{}, nil
	}
	missingAddresses := make([]string, 0, len(cfg.LAN.Networks)+1)
	var err error
	missingAddresses, err = b.appendMissingIPv6Address(missingAddresses, tunnel.InterfaceName, cfg.Tunnel.LocalIPv6)
	if err != nil {
		return nil, err
	}
	if !cfg.LAN.Enabled {
		return missingAddresses, nil
	}
	for _, network := range cfg.LAN.Networks {
		gatewayAddress, gatewayErr := gatewayForPrefix(network.Prefix)
		if gatewayErr != nil {
			return nil, gatewayErr
		}
		missingAddresses, err = b.appendMissingIPv6Address(missingAddresses, network.Interface, gatewayAddress)
		if err != nil {
			return nil, err
		}
	}
	return missingAddresses, nil
}

func (b *SystemBackend) appendMissingIPv6Address(missingAddresses []string, interfaceName string, prefixValue string) ([]string, error) {
	state, err := b.queryInterfaceIPv6Address(interfaceName, prefixValue)
	if err != nil {
		return nil, err
	}
	switch state {
	case ipv6AddressHealthy:
		return missingAddresses, nil
	case ipv6AddressUnhealthy:
		return append(missingAddresses, fmt.Sprintf("%s unhealthy %s", interfaceName, prefixValue)), nil
	default:
		return append(missingAddresses, fmt.Sprintf("%s missing %s", interfaceName, prefixValue)), nil
	}
}

type ipv6AddressState int

const (
	ipv6AddressMissing ipv6AddressState = iota
	ipv6AddressHealthy
	ipv6AddressUnhealthy
)

func (b *SystemBackend) queryInterfaceIPv6Address(interfaceName string, prefixValue string) (ipv6AddressState, error) {
	desiredPrefix, err := netip.ParsePrefix(prefixValue)
	if err != nil {
		return ipv6AddressMissing, fmt.Errorf("parse IPv6 prefix %q: %w", prefixValue, err)
	}
	output, err := b.runner.CombinedOutput("ip", "-6", "addr", "show", "dev", interfaceName)
	if err != nil {
		return ipv6AddressMissing, fmt.Errorf("read IPv6 addresses on %s: %w", interfaceName, err)
	}
	return findIPv6PrefixState(string(output), desiredPrefix), nil
}

func findIPv6PrefixState(output string, desiredPrefix netip.Prefix) ipv6AddressState {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != "inet6" {
			continue
		}
		existingPrefix, err := netip.ParsePrefix(fields[1])
		if err != nil {
			continue
		}
		if existingPrefix != desiredPrefix {
			continue
		}
		for _, flag := range fields[2:] {
			if flag == "dadfailed" || flag == "tentative" {
				return ipv6AddressUnhealthy
			}
		}
		return ipv6AddressHealthy
	}
	return ipv6AddressMissing
}

func (b *SystemBackend) run(name string, args ...string) error {
	output, err := b.runner.CombinedOutput(name, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(bytes.TrimSpace(output))))
	}
	return nil
}

func (b *SystemBackend) runBestEffort(name string, args ...string) {
	_, _ = b.runner.CombinedOutput(name, args...)
}

// ensureJump adds a -j jump rule to parentChain if not already present.
func (b *SystemBackend) ensureJump(binary string, parentChain string, targetChain string) {
	if _, err := b.runner.CombinedOutput(binary, "-C", parentChain, "-j", targetChain); err != nil {
		b.runBestEffort(binary, "-I", parentChain, "-j", targetChain)
	}
}

// removeJump removes a -j jump rule from parentChain.
func (b *SystemBackend) removeJump(binary string, parentChain string, targetChain string) {
	b.runBestEffort(binary, "-D", parentChain, "-j", targetChain)
}

func (b *SystemBackend) ensureJumpTable(binary string, table string, parentChain string, targetChain string) {
	if _, err := b.runner.CombinedOutput(binary, "-t", table, "-C", parentChain, "-j", targetChain); err != nil {
		b.runBestEffort(binary, "-t", table, "-I", parentChain, "-j", targetChain)
	}
}

func (b *SystemBackend) removeJumpTable(binary string, table string, parentChain string, targetChain string) {
	b.runBestEffort(binary, "-t", table, "-D", parentChain, "-j", targetChain)
}

func gatewayForPrefix(prefixValue string) (string, error) {
	prefix, err := netip.ParsePrefix(prefixValue)
	if err != nil {
		return "", fmt.Errorf("parse gateway prefix: %w", err)
	}
	gateway := prefix.Masked().Addr().Next()
	return gateway.String() + "/" + strconv.Itoa(prefix.Bits()), nil
}

func buildDefaultRouteArgs(cfg *config.Config) ([]string, error) {
	routeArgs := []string{"-6", "route", "replace", "::/0", "dev", tunnel.InterfaceName}
	sourceAddress, err := selectDefaultRouteSource(cfg)
	if err != nil {
		return nil, err
	}
	if sourceAddress == "" {
		return routeArgs, nil
	}
	return append(routeArgs, "src", sourceAddress), nil
}

func selectDefaultRouteSource(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", nil
	}
	tunnelAddress, err := addressFromPrefix(cfg.Tunnel.LocalIPv6)
	if err != nil {
		return "", err
	}
	if isPreferredIPv6SourceAddress(tunnelAddress) {
		return tunnelAddress.String(), nil
	}
	for _, network := range cfg.LAN.Networks {
		gatewayPrefix, err := gatewayForPrefix(network.Prefix)
		if err != nil {
			return "", err
		}
		gatewayAddress, err := addressFromPrefix(gatewayPrefix)
		if err != nil {
			return "", err
		}
		if isPreferredIPv6SourceAddress(gatewayAddress) {
			return gatewayAddress.String(), nil
		}
	}
	return "", nil
}

func addressFromPrefix(prefixValue string) (netip.Addr, error) {
	prefix, err := netip.ParsePrefix(prefixValue)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse IPv6 prefix: %w", err)
	}
	return prefix.Addr(), nil
}

func isPreferredIPv6SourceAddress(address netip.Addr) bool {
	return address.Is6() && address.IsGlobalUnicast() && !address.IsPrivate()
}

func (b *SystemBackend) resolveProbeSourceAddress(target string) (string, error) {
	output, err := b.runner.CombinedOutput("ip", "-6", "route", "get", target)
	if err != nil {
		return "", nil
	}
	match := ipv6RouteSrcPattern.FindStringSubmatch(string(output))
	if len(match) < 2 {
		return "", nil
	}
	address, err := netip.ParseAddr(match[1])
	if err != nil {
		return "", nil
	}
	if !isPreferredIPv6SourceAddress(address) {
		return "", nil
	}
	return address.String(), nil
}
