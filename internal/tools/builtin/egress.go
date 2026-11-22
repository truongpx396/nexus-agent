package builtin

import (
	"net"
	"net/http"
	"time"

	"github.com/truongpx396/nexus-agent/internal/egress"
)

// The egress policy for the tools that fetch a URL on the model's behalf lives in
// internal/egress, shared with the MCP client. These are the names this package's
// tools and tests use for it.

func newEgressClient(allowed []string, allowPrivate bool, timeout time.Duration) *http.Client {
	return egress.NewClient(allowed, allowPrivate, timeout)
}

func isPrivateIP(ip net.IP) bool { return egress.IsPrivateIP(ip) }
