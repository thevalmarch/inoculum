package presentation

import (
	"fmt"
	"net"
	"sort"
)

// ListenAddresses returns display-only candidate addresses for a coordinator
// that listens on all interfaces. It does not influence binding or discovery.
func ListenAddresses(port int) []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return []string{fmt.Sprintf(":%d", port)}
	}
	seen := make(map[string]struct{})
	var addresses []string
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		interfaceAddresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range interfaceAddresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			value := net.JoinHostPort(ip.String(), fmt.Sprint(port))
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			addresses = append(addresses, value)
		}
	}
	if len(addresses) == 0 {
		return []string{fmt.Sprintf(":%d", port)}
	}
	sort.Strings(addresses)
	return addresses
}
