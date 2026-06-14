package probe

import "net"

// OutboundIP returns the local IP address that would be used to reach
// target. Uses a UDP "connect" (no packets sent to avoid overhead) so the OS fills in
// the correct source address via routing table lookup.
// Returns an empty string if resolution fails.
func OutboundIP(target string) string {
	conn, err := net.Dial("udp", target+":80")
	if err != nil {
		conn, err = net.Dial("udp", "1.1.1.1:80")
		if err != nil {
			return ""
		}
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
