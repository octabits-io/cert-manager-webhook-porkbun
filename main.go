// Command cert-manager-webhook-porkbun runs an ACME DNS01 solver webhook for
// domains hosted at Porkbun.
package main

import (
	"fmt"
	"os"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"

	"github.com/octabits-io/cert-manager-webhook-porkbun/internal/solver"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	groupName := os.Getenv("GROUP_NAME")
	if groupName == "" {
		fmt.Fprintln(os.Stderr, "GROUP_NAME must be set to the API group this webhook serves, "+
			"matching the `groupName` in your Issuer's dns01 webhook config")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "cert-manager-webhook-porkbun %s serving group %q\n", version, groupName)

	// RunWebhookServer installs its own signal handling and calls os.Exit.
	cmd.RunWebhookServer(groupName, solver.New())
}
