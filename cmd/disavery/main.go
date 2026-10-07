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
  verify                                   verify production now
  env reset                                return the lab to its baseline
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
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") && (cmd == "drill" || cmd == "runbook" || cmd == "env" || cmd == "canary" || (cmd == "report" && args[1] == "trend")) {
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
		if err := fs.Parse(args[1:]); err != nil {
			return exitError
		}
		b, err := bia.Load(p.join(p.bia))
		if err != nil {
			return fail(err)
		}
		if err := p.lab(b, *env, nil, stdout).Reset(ctx, *env, stdout); err != nil {
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
		point, err := p.lab(b, "drill", nil, stdout).PickRestorePoint(ctx, *repo, *seed)
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
