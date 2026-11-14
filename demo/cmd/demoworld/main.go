// Command demoworld runs the demo company: its MCP tool servers (with an effects ledger) and its
// three A2A agents. Bifrost connects to the tools and registers the agents; all traffic between
// them flows through Bifrost, where Gjallarhorn governs it.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/shdra06/Gjallarhorn/demo/internal/agents"
	"github.com/shdra06/Gjallarhorn/demo/internal/tools"
)

func main() {
	gatewayURL := flag.String("gateway", envOr("GATEWAY_URL", "http://127.0.0.1:8080"), "Bifrost base URL the agents call")
	host := flag.String("host", "127.0.0.1", "address the agents and tools listen on")
	toolsAddr := flag.String("tools", "127.0.0.1:9100", "address of the MCP tool servers and the ledger")
	supportVK := flag.String("support-key", envOr("SUPPORT_VK", "sk-bf-support-demo"), "support agent's virtual key")
	billingVK := flag.String("billing-key", envOr("BILLING_VK", "sk-bf-billing-demo"), "billing agent's virtual key")
	flag.Parse()

	ledger := &tools.Ledger{}
	go func() {
		log.Printf("tools: MCP servers at http://%s/mcp/{orders,billing,email,shell,web}, ledger at /ledger", *toolsAddr)
		log.Fatal(http.ListenAndServe(*toolsAddr, tools.Handler(ledger)))
	}()

	specs := agents.Specs(*supportVK, *billingVK)
	for _, s := range specs {
		if _, err := agents.New(s, *gatewayURL).Serve(*host); err != nil {
			log.Fatalf("agent %s: %v", s.Name, err)
		}
	}
	log.Printf("agents: %s (gateway %s)", agents.Describe(specs), *gatewayURL)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
