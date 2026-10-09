// Command disavery runs disaster-recovery drills against the lab and writes
// the evidence. It runs inside the toolbox container.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/oplosy/disavery/internal/bia"
	"github.com/oplosy/disavery/internal/canary"
	"github.com/oplosy/disavery/internal/drill"
	"github.com/oplosy/disavery/internal/lab"
	"github.com/oplosy/disavery/internal/report"
	"github.com/oplosy/disavery/internal/runbook"
	"github.com/oplosy/disavery/internal/verify"
)

const usage = `usage: disavery <command> [flags]

commands:
  drill run <runbook> [--tier T] [--yes]   run a drill and write a report
  drill list                               list runbooks
  runbook lint [dir]                       validate runbooks
  report <dir>                             re-render report.md from report.json
  report trend                             summarise reports/history.jsonl
  report tiers                             compare DR tiers (site loss, S1): measured vs. target, cost
  verify                                   verify production now
  env reset [--tier T]                     return the lab to a tier's baseline topology
  env set key=value...                     change the topology (active_site, standby_site, site_b_enabled, ...)
  attachments copy --from S --to S         copy current attachments between stores (vault, obj-<site>)
  attachments rewind --store S --to T      make a site store's attachments current as of time T
  vault attack                             attack the vault with the production writer's key (S4); JSON evidence
  vault undelete --since T                 remove delete markers placed since T, as the vault administrator
  canary run                               write canary records (long-running service)
  restore-point [--repo 2] [--seed N]      choose a random backup set and PITR target (JSON)

Exit codes: 0 PASS, 2 MISSED_TARGET, 3 FAILED, 4 ERROR.
`

func main() {
	ctx, stop := signalContext()
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// signalContext is cancelled with a cause on SIGINT or SIGTERM, so a drill
// can record why it stopped.
func signalContext() (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		if s, ok := <-ch; ok {
			cancel(fmt.Errorf("interrupted by %s", s))
		}
	}()
	return ctx, func() { signal.Stop(ch); cancel(nil) }
}

const exitError = 4

// paths are the repository locations every command shares.
type paths struct {
	root, bia, runbooks, reports string
}

func (p *paths) register(fs *flag.FlagSet) {
	fs.StringVar(&p.root, "root", ".", "repository root")
	fs.StringVar(&p.bia, "bia", "docs/bia.yaml", "BIA file, relative to the root")
	fs.StringVar(&p.runbooks, "runbooks", "runbooks", "runbook directory, relative to the root")
	fs.StringVar(&p.reports, "reports", "reports", "report directory, relative to the root")
}

func (p *paths) join(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(p.root, rel)
}

func (p *paths) lab(b *bia.BIA, env string, in io.Reader, out io.Writer) *drill.LabEnv {
	e := lab.Default(p.root)
	e.Name = env
	return &drill.LabEnv{Env: e, BIA: b, In: in, Out: out}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitError
	}
	cmd := args[0]
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") && (cmd == "drill" || cmd == "runbook" || cmd == "env" || cmd == "canary" || cmd == "attachments" || cmd == "vault" || (cmd == "report" && (args[1] == "trend" || args[1] == "tiers"))) {
		cmd += " " + args[1]
		args = args[1:]
	}
	fs := flag.NewFlagSet("disavery "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var p paths
	p.register(fs)
	fail := func(err error) int {
		fmt.Fprintf(stderr, "disavery %s: %v\n", cmd, err)
		return exitError
	}

	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0

	case "drill run":
		tier := fs.String("tier", "", "DR tier, for runbooks that declare tiers")
		env := fs.String("env", "drill", "environment; must match the Terraform workspace and node labels")
		yes := fs.Bool("yes", false, "confirm a runbook that injects a failure")
		timeout := fs.Duration("timeout", time.Hour, "global timeout; cleanup still runs")
		seed := fs.Int64("seed", 0, "random seed (default: time-based)")
		push := fs.String("pushgateway", lab.Default(".").Pushgateway, "Pushgateway for the result (empty: do not push)")
		id, err := parseWithArg(fs, args[1:], "runbook id")
		if err != nil {
			return fail(err)
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		if *seed == 0 {
			*seed = time.Now().UnixNano()
		}
		r, dir, err := drill.Run(ctx, drill.Options{
			RunbookID: id, Tier: *tier, Env: *env, Yes: *yes, CI: os.Getenv("CI") == "true",
			Interactive: isTerminal(stdin), Timeout: *timeout, Seed: *seed,
			RunbookDir: p.join(p.runbooks), ReportsDir: p.join(p.reports), BIA: b, Out: stdout, Now: time.Now,
		}, p.lab(b, *env, stdin, stdout))
		if err != nil {
			fail(err)
			if r == nil {
				return exitError
			}
		}
		fmt.Fprintf(stdout, "\nreport %s\n", filepath.Join(dir, "report.md"))
		// Dashboards and alert RestoreTestStale read the result; failing to
		// push it does not change the drill's verdict.
		if *push != "" {
			pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			if err := report.Push(pctx, &http.Client{}, *push, r); err != nil {
				fmt.Fprintf(stderr, "warning: result not pushed to %s: %v\n", *push, err)
			}
			cancel()
		}
		return r.Result.ExitCode()

	case "drill list":
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		rbs, err := runbook.LoadDir(p.join(p.runbooks))
		if err != nil {
			return fail(err)
		}
		for _, rb := range rbs {
			var phases []string
			for _, ph := range rb.Phases {
				phases = append(phases, ph.Name)
			}
			tiers := strings.Join(rb.Tiers, ",")
			if tiers == "" {
				tiers = "-"
			}
			fmt.Fprintf(stdout, "%-20s tiers=%-25s phases=%-35s %s\n", rb.ID, tiers, strings.Join(phases, ","), rb.Description)
		}
		return 0

	case "runbook lint":
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		dir := p.join(p.runbooks)
		if fs.NArg() > 0 {
			dir = fs.Arg(0)
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		rbs, err := runbook.LoadDir(dir)
		if err != nil {
			return fail(err)
		}
		known := runbook.Known{Tiers: b.TierNames(), Checks: drill.CheckNames(p.lab(b, "drill", nil, stdout))}
		code := 0
		for _, rb := range rbs {
			if err := runbook.Lint(rb, known); err != nil {
				fmt.Fprintf(stdout, "FAIL %s\n%s\n", rb.Path, indent(err.Error()))
				code = exitError
				continue
			}
			fmt.Fprintf(stdout, "ok   %s\n", rb.Path)
		}
		if len(rbs) == 0 {
			return fail(fmt.Errorf("no runbooks in %s", dir))
		}
		return code

	case "report trend":
		history := fs.String("history", "", "history file (default <reports>/history.jsonl)")
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		path := *history
		if path == "" {
			path = filepath.Join(p.join(p.reports), "history.jsonl")
		}
		entries, err := report.ReadHistory(path)
		if err != nil {
			return fail(err)
		}
		fmt.Fprint(stdout, report.RenderTrend(report.Trend(entries)))
		return 0

	case "report":
		dir, err := parseWithArg(fs, args[1:], "report directory")
		if err != nil {
			return fail(err)
		}
		r, err := report.ReadFile(filepath.Join(dir, "report.json"))
		if err != nil {
			return fail(err)
		}
		if err := r.WriteDir(dir); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, filepath.Join(dir, "report.md"))
		return 0

	case "verify":
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		results, err := p.lab(b, "drill", nil, stdout).ProductionChecks(ctx)
		code := 0
		for _, res := range results {
			fmt.Fprintf(stdout, "%-22s %-4s %s\n", res.Name, res.Status, res.Summary)
			for _, d := range res.Details {
				fmt.Fprintf(stdout, "%27s%s\n", "", d)
			}
			if res.Status == verify.Fail {
				code = report.Failed.ExitCode()
			}
		}
		if err != nil {
			return fail(err)
		}
		return code

	case "env reset":
		env := fs.String("env", "drill", "environment; must match the Terraform workspace and node labels")
		tier := fs.String("tier", "", "DR tier whose baseline topology to apply (default: keep the current topology)")
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		if err := p.lab(b, *env, nil, stdout).Reset(ctx, *env, *tier, stdout); err != nil {
			return fail(err)
		}
		return 0

	case "env set":
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		e := lab.Default(p.root)
		top, err := lab.ReadTopology(e.TopologyPath)
		if err != nil {
			return fail(err)
		}
		for _, kv := range fs.Args() {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fail(fmt.Errorf("expected key=value, got %q", kv))
			}
			if err := top.Set(k, v); err != nil {
				return fail(err)
			}
		}
		if err := lab.WriteTopology(e.TopologyPath, top); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "active_site=%s standby_site=%s site_a_enabled=%t site_b_enabled=%t\n",
			top.ActiveSite, top.StandbySite, top.SiteAEnabled, top.SiteBEnabled)
		return 0

	case "report tiers":
		history := fs.String("history", "", "history file (default <reports>/history.jsonl)")
		scenario := fs.String("scenario", "s1-site-loss", "scenario whose runs are compared")
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		path := *history
		if path == "" {
			path = filepath.Join(p.join(p.reports), "history.jsonl")
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		entries, err := report.ReadHistory(path)
		if err != nil {
			return fail(err)
		}
		fmt.Fprint(stdout, report.RenderTierComparison(report.CompareTiers(entries, *scenario, tierSpecs(b))))
		return 0

	case "attachments copy":
		from := fs.String("from", "vault", "source store: vault or obj-<site>")
		to := fs.String("to", "", "target store: obj-<site>")
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		if *to == "" {
			return fail(fmt.Errorf("--to is required"))
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		st, err := p.lab(b, "drill", nil, stdout).CopyAttachments(ctx, *from, *to)
		fmt.Fprintf(stdout, "copied %d (%d as vault replicas), already present %d\n", st.Copied, st.Replicas, st.Skipped)
		if err != nil {
			return fail(err)
		}
		return 0

	case "attachments rewind":
		store := fs.String("store", "", "site store to rewind: obj-<site>")
		to := fs.String("to", "", "point in time (RFC 3339 or PostgreSQL timestamptz text)")
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		if *store == "" || *to == "" {
			return fail(fmt.Errorf("--store and --to are required"))
		}
		at, err := verify.ParseTime(*to)
		if err != nil {
			return fail(err)
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		st, err := p.lab(b, "drill", nil, stdout).RewindAttachments(ctx, *store, at)
		fmt.Fprintf(stdout, "restored %d, unchanged %d, created later %d\n", st.Restored, st.Unchanged, st.Newer)
		if err != nil {
			return fail(err)
		}
		return 0

	case "vault attack":
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		ev, err := p.lab(b, "drill", nil, stdout).AttackVault(ctx)
		for _, a := range ev.Attempts {
			fmt.Fprintf(stderr, "%-24s %-11s denied %d of %d %v\n", a.Action, a.Bucket, a.Denied, a.Tried, a.Codes)
		}
		fmt.Fprintf(stderr, "hid %d objects behind delete markers\n", ev.Hidden)
		if err != nil {
			return fail(err)
		}
		if err := json.NewEncoder(stdout).Encode(ev); err != nil {
			return fail(err)
		}
		if breached := ev.Breached(); len(breached) > 0 {
			fmt.Fprintf(stderr, "disavery vault attack: the vault allowed %s\n", strings.Join(breached, ", "))
			return report.Failed.ExitCode()
		}
		return 0

	case "vault undelete":
		since := fs.String("since", "", "remove delete markers placed at or after this time (RFC 3339 or PostgreSQL timestamptz text)")
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		at, err := verify.ParseTime(*since)
		if err != nil {
			return fail(fmt.Errorf("--since: %w", err))
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		n, err := p.lab(b, "drill", nil, stdout).UndeleteVault(ctx, at)
		fmt.Fprintf(stdout, "removed %d delete markers\n", n)
		if err != nil {
			return fail(err)
		}
		return 0

	case "canary run":
		interval := fs.Duration("interval", time.Second, "time between writes")
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		if err := runCanary(ctx, lab.Default(p.root), *interval, stdout); err != nil {
			return fail(err)
		}
		return 0

	case "restore-point":
		repo := fs.Int("repo", 2, "pgBackRest repository to restore from")
		seed := fs.Int64("seed", 0, "random seed (default: time-based)")
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		if *seed == 0 {
			*seed = time.Now().UnixNano()
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		history, err := report.ReadHistory(filepath.Join(p.join(p.reports), "history.jsonl"))
		if err != nil {
			return fail(err)
		}
		point, err := p.lab(b, "drill", nil, stdout).PickRestorePoint(ctx, *repo, *seed, drill.DisruptiveWindows(history, b))
		if err != nil {
			return fail(err)
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(point); err != nil {
			return fail(err)
		}
		return 0
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
	return exitError
}

// parseWithArg parses flags that may come before or after one positional argument.
func parseWithArg(fs *flag.FlagSet, args []string, name string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() == 0 {
		return "", fmt.Errorf("missing %s", name)
	}
	arg := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil {
		return "", err
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	return arg, nil
}

func runCanary(ctx context.Context, env *lab.Env, interval time.Duration, out io.Writer) error {
	log := slog.New(slog.NewJSONHandler(out, nil))
	client, err := env.HTTPClient(ctx, 5*time.Second)
	if err != nil {
		return err
	}
	entries, err := canary.ReadJournal(env.JournalPath)
	if err != nil {
		return err
	}
	var last int64
	if len(entries) > 0 {
		last = entries[len(entries)-1].Seq
	}
	j, err := canary.OpenJournal(env.JournalPath)
	if err != nil {
		return err
	}
	defer j.Close()
	w := &canary.Writer{URL: env.PublicURL + "/canary", Client: client, Interval: interval, Journal: j, Now: time.Now, Log: log}
	seq := canary.NextSeq(last, time.Now())
	log.Info("canary writing", "url", w.URL, "journal", env.JournalPath, "first_seq", seq)
	return w.Run(ctx, seq)
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func indent(s string) string { return "  " + strings.ReplaceAll(s, "\n", "\n  ") }

// tierSpecs turns the BIA tiers into the report's comparison input.
func tierSpecs(b *bia.BIA) []report.TierSpec {
	var out []report.TierSpec
	for _, name := range b.TierNames() {
		t := b.Tiers[name]
		out = append(out, report.TierSpec{
			Name: name, TargetRPO: t.RPO.D(), TargetRTO: t.RTO.D(),
			AlwaysOnNodes: t.Cost.AlwaysOnNodes, MonthlyUSD: t.Cost.MonthlyUSD,
		})
	}
	return out
}
