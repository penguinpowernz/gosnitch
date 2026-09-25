package main

// Development helper: prints the parsed rules exactly as the UI would order them.

import (
	"flag"
	"fmt"
	"log"

	"github.com/penguinpowernz/gosnitch/internal/daemon"
)

func main() {
	dir := flag.String("rules", daemon.DefaultRulesPath, "rules directory")
	n := flag.Int("n", 10, "how many to print")
	flag.Parse()

	rs := daemon.NewRuleStore(*dir)
	if err := rs.Reload(); err != nil {
		log.Fatal(err)
	}
	all := rs.Snapshot()
	fmt.Printf("parsed %d rules\n\n", len(all))
	for i, r := range all {
		if i >= *n {
			break
		}
		fmt.Printf("%s  %-6s %-13s %-28s %-22s %-6s uid=%s\n",
			r.Created.Format("2006-01-02 15:04"), r.Action, r.Duration,
			truncate(r.Process, 28), truncate(r.Dest, 22), r.Port, r.UserID)
	}

	var noProc int
	for _, r := range all {
		if r.Process == "" {
			noProc++
		}
	}
	fmt.Printf("\nrules with no process operand: %d\n", noProc)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n+1:]
}
