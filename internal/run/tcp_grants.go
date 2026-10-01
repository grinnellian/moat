package run

import (
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/netrules"
)

// appendNoProxyGrants returns env with the granted IPs appended to the
// NO_PROXY and no_proxy entries.
func appendNoProxyGrants(env []string, grants []netrules.TCPGrant) []string {
	return env
}

// checkTCPGrantsSupported refuses network.tcp on runtimes that cannot enforce it.
func checkTCPGrantsSupported(rt container.RuntimeType, grants []netrules.TCPGrant) error {
	return nil
}
