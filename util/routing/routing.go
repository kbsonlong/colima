package routing

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/abiosoft/colima/config"
	"github.com/abiosoft/colima/environment/host"
	"github.com/abiosoft/colima/environment/vm/lima"
	"github.com/abiosoft/colima/environment/vm/lima/limautil"
	"github.com/abiosoft/colima/util"
	log "github.com/sirupsen/logrus"
)

// RouteManager manages network routing rules for Pod networks
type RouteManager struct {
	vmIP    string
	podCIDR string
	profile string
}

// NewRouteManager creates a new route manager instance
func NewRouteManager(vmIP, podCIDR, profile string) *RouteManager {
	return &RouteManager{
		vmIP:    vmIP,
		podCIDR: podCIDR,
		profile: profile,
	}
}

// SetupPodRouting configures routing rules for Pod network access
func (rm *RouteManager) SetupPodRouting(ctx context.Context) error {
	if !util.MacOS() {
		log.Debug("Pod routing setup is only supported on macOS")
		return nil
	}

	if rm.vmIP == "" || rm.podCIDR == "" {
		log.Debug("VM IP or Pod CIDR not available, skipping Pod routing setup")
		return nil
	}

	log.Infof("Setting up Pod network routing: %s -> %s", rm.podCIDR, rm.vmIP)

	// Check if route already exists
	if rm.routeExists() {
		log.Debug("Pod network route already exists")
		return nil
	}

	// Add route
	cmd := exec.CommandContext(ctx, "sudo", "route", "add", rm.podCIDR, rm.vmIP)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add Pod network route: %w, output: %s", err, string(output))
	}

	log.Infof("✅ Pod network route configured successfully: %s -> %s", rm.podCIDR, rm.vmIP)
	return nil
}

// CleanupPodRouting removes routing rules for Pod network
func (rm *RouteManager) CleanupPodRouting(ctx context.Context) error {
	if !util.MacOS() {
		log.Debug("Pod routing cleanup is only supported on macOS")
		return nil
	}

	if rm.podCIDR == "" {
		log.Debug("Pod CIDR not available, skipping Pod routing cleanup")
		return nil
	}

	log.Infof("Cleaning up Pod network routing: %s", rm.podCIDR)

	// Check if route exists before trying to delete
	if !rm.routeExists() {
		log.Debug("Pod network route does not exist, nothing to cleanup")
		return nil
	}

	// Remove route
	cmd := exec.CommandContext(ctx, "sudo", "route", "delete", rm.podCIDR)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Don't treat route deletion failure as fatal
		log.Warnf("Failed to remove Pod network route: %v, output: %s", err, string(output))
		return nil
	}

	log.Infof("✅ Pod network route cleaned up successfully: %s", rm.podCIDR)
	return nil
}

// routeExists checks if the Pod network route already exists
func (rm *RouteManager) routeExists() bool {
	cmd := exec.Command("route", "-n", "get", rm.podCIDR)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}

	// Check if the route points to our VM IP
	return strings.Contains(string(output), rm.vmIP)
}

// GetVMIP retrieves the VM IP address for the current profile
func GetVMIP(ctx context.Context, profile string) (string, error) {
	if !util.MacOS() {
		return "", fmt.Errorf("VM IP detection is only supported on macOS")
	}

	// Use limautil.IPAddress to get VM IP (same as getStatus method)
	ipAddress := limautil.IPAddress(profile)
	if ipAddress == "" || ipAddress == "127.0.0.1" {
		return "", fmt.Errorf("VM IP not available or is localhost")
	}

	// Validate IP address
	if net.ParseIP(ipAddress) == nil {
		return "", fmt.Errorf("invalid VM IP address: %s", ipAddress)
	}

	return ipAddress, nil
}

// GetPodCIDR retrieves the Pod network CIDR from the Kubernetes cluster
func GetPodCIDR(ctx context.Context) (string, error) {
	// Create lima VM instance to execute commands
	guest := lima.New(host.New())

	// Check if VM is running
	if !guest.Running(ctx) {
		return "", fmt.Errorf("VM not running")
	}

	// Method 1: Try to get Pod CIDR from k3s cluster info dump
	output, err := guest.RunOutput("kubectl", "cluster-info", "dump")
	if err == nil {
		// Parse cluster-cidr from output
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "cluster-cidr=") {
				// Extract CIDR value
				start := strings.Index(line, "cluster-cidr=") + len("cluster-cidr=")
				remaining := line[start:]
				// Handle quoted values
				if strings.HasPrefix(remaining, `"`) {
					end := strings.Index(remaining[1:], `"`)
					if end > 0 {
						cidr := remaining[1 : end+1]
						if _, _, err := net.ParseCIDR(cidr); err == nil {
							return cidr, nil
						}
					}
				} else {
					// Handle unquoted values
					fields := strings.Fields(remaining)
					if len(fields) > 0 {
						cidr := fields[0]
						if _, _, err := net.ParseCIDR(cidr); err == nil {
							return cidr, nil
						}
					}
				}
			}
		}
	}

	// Method 2: Try to get from flannel configmap
	output, err = guest.RunOutput("kubectl", "get", "configmap", "kube-flannel-cfg", "-n", "kube-system", "-o", "yaml")
	if err == nil {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Network") && strings.Contains(line, ":") {
				parts := strings.Split(line, ":")
				if len(parts) > 1 {
					cidr := strings.TrimSpace(parts[1])
					cidr = strings.Trim(cidr, `"'`)
					if _, _, err := net.ParseCIDR(cidr); err == nil {
						return cidr, nil
					}
				}
			}
		}
	}

	// Fallback to default k3s Pod CIDR
	log.Debug("Failed to get Pod CIDR from cluster, using default k3s CIDR")
	return "10.42.0.0/16", nil
}

// SetupPodRoutingForProfile sets up Pod network routing for a specific profile
func SetupPodRoutingForProfile(ctx context.Context, conf config.Config) error {
	// Only setup routing if Kubernetes is enabled and network.address is used
	if !conf.Kubernetes.Enabled {
		log.Debug("Kubernetes not enabled, skipping Pod routing setup")
		return nil
	}

	if !conf.Network.Address {
		log.Debug("Neither network.address nor network address enabled, skipping Pod routing setup")
		return nil
	}

	profile := config.CurrentProfile().ID

	// Get VM IP
	vmIP, err := GetVMIP(ctx, profile)
	if err != nil {
		log.Warnf("Failed to get VM IP for Pod routing: %v", err)
		return nil // Don't fail startup for routing issues
	}

	// Get Pod CIDR
	podCIDR, err := GetPodCIDR(ctx)
	if err != nil {
		log.Warnf("Failed to get Pod CIDR for routing: %v", err)
		return nil // Don't fail startup for routing issues
	}

	// Setup routing
	rm := NewRouteManager(vmIP, podCIDR, profile)
	return rm.SetupPodRouting(ctx)
}

// CleanupPodRoutingForProfile cleans up Pod network routing for a specific profile
func CleanupPodRoutingForProfile(ctx context.Context, conf config.Config) error {
	// Only cleanup routing if Kubernetes was enabled
	if !conf.Kubernetes.Enabled {
		log.Debug("Kubernetes not enabled, skipping Pod routing cleanup")
		return nil
	}

	profile := config.CurrentProfile().ID

	// Get Pod CIDR (we don't need VM IP for cleanup)
	podCIDR, err := GetPodCIDR(ctx)
	if err != nil {
		log.Warnf("Failed to get Pod CIDR for routing cleanup: %v", err)
		// Try with default CIDR
		podCIDR = "10.42.0.0/16"
	}

	// Cleanup routing
	rm := NewRouteManager("", podCIDR, profile)
	return rm.CleanupPodRouting(ctx)
}
