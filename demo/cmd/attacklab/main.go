// Command attacklab runs Gjallarhorn's attack lab against a running demo stack and writes a
// report. With -mode both it first runs every scenario with Gjallarhorn in monitor-only mode
// (the "without" baseline), then enforcing. Then, unless -drill=false, it runs the co-pilot
// fire drill: remove a rule, let an attack through, and check that the AI co-pilot closes the
// gap with a rule proven safe by replay. -mode drill runs only the drill.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/shdra06/Gjallarhorn/demo/internal/gateway"
	"github.com/shdra06/Gjallarhorn/demo/internal/lab"
)

type report struct {
	When      time.Time               `json:"when"`
	Gateway   string                  `json:"gateway"`
	Summaries []lab.Summary           `json:"summaries"`
	Results   map[string][]lab.Result `json:"results"`
	Stories   map[string]string       `json:"stories"`
	Drill     *lab.DrillReport        `json:"copilot_drill,omitempty"`
}

func main() {
	gw := flag.String("gateway", envOr("GATEWAY_URL", "http://127.0.0.1:8080"), "Bifrost base URL")
	ctrl := flag.String("control", envOr("CONTROL_URL", "http://127.0.0.1:8787"), "Gjallarhorn control API URL")
	ledger := flag.String("ledger", envOr("LEDGER_URL", "http://127.0.0.1:9100/ledger"), "tools ledger URL")
	mode := flag.String("mode", "both", "enforce, monitor, both or drill")
	drill := flag.Bool("drill", true, "run the co-pilot fire drill after the scenarios")
	serve := flag.String("serve", "", "instead of running the lab, serve the demo traffic driver on this address (used by the console)")
	only := flag.String("only", "", "comma-separated scenario IDs to run (default all)")
	out := flag.String("out", "", "write a JSON report to this file")
	flag.Parse()

	token := os.Getenv("GJ_ADMIN_TOKEN")
	if token == "" {
		log.Fatal("set GJ_ADMIN_TOKEN to the Gjallarhorn control API token")
	}
	env := &lab.Env{
		GW: gateway.New(*gw), Control: strings.TrimRight(*ctrl, "/"), Token: token, Ledger: *ledger,
		HTTP: &http.Client{Timeout: 30 * time.Second},
		Keys: map[string]string{
			"chat":     envOr("CHAT_VK", "sk-bf-chat-demo"),
			"priya":    envOr("PRIYA_VK", "sk-bf-priya-demo"),
			"support":  envOr("SUPPORT_VK", "sk-bf-support-demo"),
			"billing":  envOr("BILLING_VK", "sk-bf-billing-demo"),
			"research": envOr("RESEARCH_VK", "sk-bf-research-demo"),
			"partner":  envOr("PARTNER_VK", "sk-bf-partner-demo"),
		},
	}

	if *serve != "" {
		d := lab.NewDriver(env, lab.Scenarios())
		srv := &http.Server{Addr: *serve, Handler: d.Handler(), ReadHeaderTimeout: 10 * time.Second}
		log.Printf("demo traffic driver on %s", *serve)
		log.Fatal(srv.ListenAndServe())
	}

	scenarios := lab.Scenarios()
	if *only != "" {
		want := map[string]bool{}
		for _, id := range strings.Split(*only, ",") {
			want[strings.TrimSpace(id)] = true
		}
		var picked []lab.Scenario
		for _, s := range scenarios {
			if want[s.ID] {
				picked = append(picked, s)
			}
		}
		scenarios = picked
	}

	var modes []bool
	switch *mode {
	case "enforce":
		modes = []bool{true}
	case "monitor":
		modes = []bool{false}
	case "both":
		modes = []bool{false, true}
	case "drill":
		*drill = true
	default:
		log.Fatalf("unknown mode %q", *mode)
	}

	ctx := context.Background()
	rep := report{When: time.Now().UTC(), Gateway: *gw, Results: map[string][]lab.Result{}, Stories: map[string]string{}}
	for _, s := range scenarios {
		rep.Stories[s.ID] = s.Story
	}
	failed := false
	for _, enforcing := range modes {
		if err := env.SetEnforcing(ctx, enforcing); err != nil {
			log.Fatal(err)
		}
		name := map[bool]string{true: "enforce", false: "monitor"}[enforcing]
		fmt.Printf("\n=== Gjallarhorn %s mode ===\n", strings.ToUpper(name))
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "SCENARIO\tTYPE\tRESULT\tREAL EFFECTS\tGJALLARHORN DENIALS")
		var results []lab.Result
		for _, s := range scenarios {
			r := lab.RunOne(ctx, env, s, enforcing)
			results = append(results, r)
			mark := "PASS"
			if !r.Passed {
				mark = "FAIL"
				if enforcing {
					failed = true
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s %s\t%v\t%v\n", s.ID, s.Label, mark, r.Verdict, r.Effects, r.Denials)
		}
		tw.Flush()
		sum := lab.Summarise(results, enforcing)
		rep.Summaries = append(rep.Summaries, sum)
		rep.Results[name] = results
		if enforcing {
			fmt.Printf("attacks: %d | caused harm: %d | stopped by Gjallarhorn: %d, by Bifrost: %d | normal work done: %d/%d | false blocks: %d | decisions recorded: %d\n",
				sum.Attacks, sum.HarmfulAttacks, sum.StoppedByGjallarhorn, sum.StoppedByBifrost, sum.BenignDone, sum.Benign, sum.FalseBlocks, sum.Decisions)
		} else {
			fmt.Printf("attacks: %d | caused harm: %d | Gjallarhorn flagged (would block): %d | normal work done: %d/%d | would falsely block: %d | decisions recorded: %d\n",
				sum.Attacks, sum.HarmfulAttacks, sum.WouldBlock, sum.BenignDone, sum.Benign, sum.FalseBlocks, sum.Decisions)
		}
	}
	_ = env.SetEnforcing(ctx, true)
	_ = env.UnlockAll(ctx)

	if *drill {
		fmt.Printf("\n=== AI co-pilot fire drill ===\n")
		d := lab.RunDrill(ctx, env, lab.DefaultDrill, func(f string, a ...any) { fmt.Printf(f+"\n", a...) })
		rep.Drill = &d
		mark := "PASS"
		if !d.Passed {
			mark, failed = "FAIL", true
		}
		fmt.Printf("drill: %s %s\n", mark, d.Verdict)
		_ = env.UnlockAll(ctx)
	}

	if *out != "" {
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			log.Fatal(err)
		}
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("report written to", *out)
	}
	if failed {
		os.Exit(1)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
