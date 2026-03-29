package control

import (
	"bytes"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

const (
	iptablesChainInput   = "IPV6TUNNEL_INPUT"
	iptablesChainForward = "IPV6TUNNEL_FORWARD"
	iptablesChainMSS     = "IPV6TUNNEL_MSS"
)

var (
	ipv4AddressPattern = regexp.MustCompile(`inet (\d+\.\d+\.\d+\.\d+)`)
	pingTimePattern    = regexp.MustCompile(`time=([0-9.]+)`)
)

// Backend observes and applies the dataplane state.
type Backend interface {
	Reconcile(input ReconcileInput) error
	Observe(input ObserveInput) (*Observation, error)
	Probe(target string) (ProbeResult, error)
}

// ReconcileInput describes the desired dataplane state.
type ReconcileInput struct {
	Config      *config.Config
	ConfigValid bool
	Force       bool
	Applied     AppliedState
}

// ObserveInput describes the current read-only dataplane observation request.
type ObserveInput struct {
	WANInterface string
}

// Observation holds a lightweight dataplane observation.
type Observation struct {
	TunnelUp bool
	WANIPv4  string
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
	if err := b.run("ip", "-6", "addr", "add", input.Config.Tunnel.LocalIPv6, "dev", tunnel.InterfaceName); err != nil {
		return err
	}
	if err := b.run("ip", "-6", "route", "replace", "::/0", "dev", tunnel.InterfaceName); err != nil {
		return err
	}
	if err := b.run("sysctl", "-w", "net.ipv6.conf.all.forwarding=1"); err != nil {
		return err
	}
	if input.Config.LAN.Enabled {
		for _, network := range input.Config.LAN.Networks {
			gatewayAddress, err := gatewayForPrefix(network.Prefix)
			if err != nil {
				return err
			}
			if err := b.run("ip", "-6", "addr", "add", gatewayAddress, "dev", network.Interface); err != nil {
				return err
			}
		}
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

// Observe reads the lightweight runtime state.
func (b *SystemBackend) Observe(input ObserveInput) (*Observation, error) {
	observation := &Observation{}
	if _, err := b.runner.CombinedOutput("ip", "link", "show", tunnel.InterfaceName); err == nil {
		observation.TunnelUp = true
	}
	if strings.TrimSpace(input.WANInterface) == "" {
		return observation, nil
	}
	wanIP, err := b.getWANIPv4(input.WANInterface)
	if err == nil {
		observation.WANIPv4 = wanIP
	}
	return observation, nil
}

// Probe runs a single IPv6 ping probe.
func (b *SystemBackend) Probe(target string) (ProbeResult, error) {
	output, err := b.runner.CombinedOutput("ping", "-6", "-c", "1", "-W", "3", target)
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
