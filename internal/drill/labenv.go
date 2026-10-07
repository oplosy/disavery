package drill

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/oplosy/disavery/internal/bia"
	"github.com/oplosy/disavery/internal/canary"
	"github.com/oplosy/disavery/internal/executor"
	"github.com/oplosy/disavery/internal/lab"
	"github.com/oplosy/disavery/internal/objcopy"
	"github.com/oplosy/disavery/internal/prober"
	"github.com/oplosy/disavery/internal/report"
	"github.com/oplosy/disavery/internal/restorepoint"
	"github.com/oplosy/disavery/internal/runbook"
	"github.com/oplosy/disavery/internal/verify"
)

// LabEnv is the real lab, reached from the toolbox.
type LabEnv struct {
	Env *lab.Env
	BIA *bia.BIA
	In  io.Reader // operator input for manual steps
	Out io.Writer
}

func (l *LabEnv) psql(db string) verify.PSQL { return verify.PSQL{Remote: l.Env.SSH, Database: db} }

// DBHost implements Lab.
func (l *LabEnv) DBHost() (string, error) {
	inv, err := l.Env.Inventory()
	if err != nil {
		return "", err
	}
	return inv.DBHost(), nil
}

// Safety implements Lab: the Terraform workspace must be env and every
// inventory node must carry the label disavery.env=<env>.
func (l *LabEnv) Safety(ctx context.Context, env string) error {
	ws := "default"
	if b, err := os.ReadFile(filepath.Join(l.Env.TerraformDir, ".terraform", "environment")); err == nil {
		ws = strings.TrimSpace(string(b))
	}
	if ws != env {
		return fmt.Errorf("terraform workspace is %q but --env is %q", ws, env)
	}
	inv, err := l.Env.Inventory()
	if err != nil {
		return err
	}
	hosts := inv.HostNames()
	args := append([]string{"inspect", "--format", `{{.Name}} {{index .Config.Labels "disavery.env"}}`}, hosts...)
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("inspect lab containers: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var wrong []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		name, label, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
		if label != env {
			wrong = append(wrong, fmt.Sprintf("%s (label %q)", name, label))
		}
	}
	if len(wrong) > 0 {
		return fmt.Errorf("containers not labelled disavery.env=%s: %s", env, strings.Join(wrong, ", "))
	}
	return nil
}

// Preflight implements Lab (spec §9). Runbooks add their own preflight checks
// (expected topology, replica health).
func (l *LabEnv) Preflight(ctx context.Context) []report.Check {
	pf := l.BIA.Preflight
	checks := []struct {
		name string
		run  func(context.Context) (string, error)
	}{
		{"app-ready", l.appReady},
		{"backups-fresh", func(ctx context.Context) (string, error) { return l.backupsFresh(ctx, pf.MaxBackupAge.D()) }},
		{"wal-archive", func(ctx context.Context) (string, error) { return l.walArchive(ctx, pf.MaxArchiveAge.D()) }},
		{"canary-writing", func(context.Context) (string, error) { return l.canaryWriting(pf.MaxCanarySilence.D()) }},
		{"vault-reachable", l.vaultReachable},
		{"escrow-present", func(context.Context) (string, error) { return l.escrowPresent() }},
		{"no-firing-alerts", l.noFiringAlerts},
	}
	out := make([]report.Check, 0, len(checks))
	for _, c := range checks {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		detail, err := c.run(cctx)
		cancel()
		status := "pass"
		if err != nil {
			status, detail = "fail", err.Error()
		}
		out = append(out, report.Check{Name: c.name, Status: status, Detail: detail})
	}
	return out
}

func (l *LabEnv) get(ctx context.Context, url string) (int, []byte, error) {
	c, err := l.Env.HTTPClient(ctx, 10*time.Second)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return resp.StatusCode, body, err
}

func (l *LabEnv) appReady(ctx context.Context) (string, error) {
	url := l.Env.PublicURL + "/readyz"
	code, body, err := l.get(ctx, url)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("%s answered %d: %s", url, code, bytes.TrimSpace(body))
	}
	return url + " answered 200", nil
}

func (l *LabEnv) backupInfo(ctx context.Context) ([]byte, error) {
	host, err := l.DBHost()
	if err != nil {
		return nil, err
	}
	out, err := l.Env.SSH.Run(ctx, host, "runuser -u postgres -- pgbackrest --stanza=main info --output=json", nil)
	return []byte(out), err
}

func (l *LabEnv) backupsFresh(ctx context.Context, maxAge time.Duration) (string, error) {
	info, err := l.backupInfo(ctx)
	if err != nil {
		return "", err
	}
	now := l.Env.Now()
	var parts []string
	for _, repo := range []int{1, 2} {
		backups, err := restorepoint.ParseInfo(info, repo)
		if err != nil {
			return "", err
		}
		if len(backups) == 0 {
			return "", fmt.Errorf("repo%d has no backup", repo)
		}
		newest := backups[len(backups)-1]
		age := now.Sub(newest.Stop)
		if age > maxAge {
			return "", fmt.Errorf("newest backup in repo%d is %s old (limit %s)", repo, age.Round(time.Second), maxAge)
		}
		parts = append(parts, fmt.Sprintf("repo%d %s %s ago", repo, newest.Type, age.Round(time.Second)))
	}
	return "newest backups: " + strings.Join(parts, ", "), nil
}

func (l *LabEnv) walArchive(ctx context.Context, maxAge time.Duration) (string, error) {
	host, err := l.DBHost()
	if err != nil {
		return "", err
	}
	rows, err := l.psql("postgres").Query(ctx, host, `SELECT
  coalesce(extract(epoch FROM now() - last_archived_time), -1),
  coalesce(last_failed_time > last_archived_time, false)
FROM pg_stat_archiver`)
	if err != nil {
		return "", err
	}
	if len(rows) != 1 || len(rows[0]) != 2 {
		return "", fmt.Errorf("unexpected pg_stat_archiver result %v", rows)
	}
	secs, err := strconv.ParseFloat(rows[0][0], 64)
	if err != nil {
		return "", err
	}
	age := time.Duration(secs * float64(time.Second))
	switch {
	case secs < 0:
		return "", errors.New("no WAL segment has been archived yet")
	case rows[0][1] == "t":
		return "", errors.New("the latest archive attempt failed")
	case age > maxAge:
		return "", fmt.Errorf("last WAL segment archived %s ago (limit %s)", age.Round(time.Second), maxAge)
	}
	return fmt.Sprintf("last WAL segment archived %s ago", age.Round(time.Second)), nil
}

func (l *LabEnv) journal() ([]canary.Entry, error) { return canary.ReadJournal(l.Env.JournalPath) }

func (l *LabEnv) canaryWriting(maxSilence time.Duration) (string, error) {
	entries, err := l.journal()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("canary journal %s is empty; is the canary service running?", l.Env.JournalPath)
	}
	last := entries[len(entries)-1]
	silence := l.Env.Now().Sub(last.Acked)
	if silence > maxSilence {
		return "", fmt.Errorf("last acknowledged canary write was %s ago (limit %s)", silence.Round(time.Second), maxSilence)
	}
	return fmt.Sprintf("last acknowledged canary write %s ago (seq %d)", silence.Round(time.Millisecond), last.Seq), nil
}

func (l *LabEnv) vaultReachable(ctx context.Context) (string, error) {
	url := "https://" + l.Env.VaultEndpoint + "/minio/health/live"
	code, _, err := l.get(ctx, url)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("%s answered %d", url, code)
	}
	return url + " answered 200", nil
}

func (l *LabEnv) escrowPresent() (string, error) {
	key, err := os.ReadFile(l.Env.AgeKeyFile)
	if err != nil {
		return "", err
	}
	escrow, err := os.ReadFile(l.Env.EscrowFile)
	if err != nil {
		return "", err
	}
	if len(escrow) == 0 || !bytes.Equal(key, escrow) {
		return "", fmt.Errorf("%s does not match the current age key", l.Env.EscrowFile)
	}
	return l.Env.EscrowFile + " matches the current age key", nil
}

// Prober implements Lab.
func (l *LabEnv) Prober(ctx context.Context) (*prober.Prober, error) {
	c, err := l.Env.HTTPClient(ctx, 3*time.Second)
	if err != nil {
		return nil, err
	}
	return &prober.Prober{BaseURL: l.Env.PublicURL, Client: c, Interval: 500 * time.Millisecond, Timeout: 3 * time.Second, Now: l.Env.Now}, nil
}

// Runners implements Lab.
func (l *LabEnv) Runners(interactive bool) map[string]executor.Runner {
	m := manualRunner{Out: l.Out, Now: l.Env.Now}
	if interactive && l.In != nil {
		m.Lines = readLines(l.In)
	}
	return map[string]executor.Runner{
		runbook.KindRun:       runRunner{Dir: l.Env.Root},
		runbook.KindSSH:       sshRunner{SSH: l.Env.SSH},
		runbook.KindWaitHTTP:  waitHTTPRunner{Client: func(ctx context.Context) (*http.Client, error) { return l.Env.HTTPClient(ctx, 5*time.Second) }},
		runbook.KindSleep:     sleepRunner{},
		runbook.KindManual:    m,
		runbook.KindWaitAlert: waitAlertRunner{URL: l.Env.Alertmanager, Client: &http.Client{Timeout: 5 * time.Second}},
	}
}

// Checks implements Lab.
func (l *LabEnv) Checks(rc RunContext) verify.Registry {
	sql := l.psql("docsvc")
	samples := rc.Samples
	if samples == nil {
		samples = func() []prober.Sample { return nil }
	}
	return verify.Registry{
		"amcheck":               verify.Amcheck{Remote: l.Env.SSH, Bin: "/usr/lib/postgresql/16/bin/pg_amcheck"},
		"business-rules":        verify.Rules{SQL: sql},
		"db-object-consistency": verify.Consistency{SQL: sql, Stores: l.store},
		"pitr":                  verify.PITR{SQL: sql, Journal: l.journal, Tolerance: l.BIA.RestoreTest.PITRTolerance.D()},
		"canary":                verify.LiveRPO{Journal: l.journal, Survivors: l.survivors, Target: rc.Targets.RPO},
		"prober":                verify.LiveRTO{Samples: samples, Target: rc.Targets.RTO, Streak: 5},
		"replication":           verify.Replication{SQL: l.psql("postgres")},
		"topology":              verify.Topology{Current: l.topology},
		"store-sync":            verify.StoreSync{Stores: l.store},
		"webhook-roundtrip": verify.WebhookRoundTrip{BaseURL: l.Env.PublicURL, Poll: 500 * time.Millisecond,
			Client: func(ctx context.Context) (*http.Client, error) { return l.Env.HTTPClient(ctx, 10*time.Second) }},
	}
}

// minioClient opens an attachment store: "vault" with the production writer's
// (read-capable) credentials, or a site store such as "obj-a" as its root.
func (l *LabEnv) minioClient(ctx context.Context, name string) (*minio.Client, error) {
	s, err := l.Env.Secrets(ctx)
	if err != nil {
		return nil, err
	}
	creds := func(user, pass string) (*credentials.Credentials, error) {
		u, err := s.String("minio", user)
		if err != nil {
			return nil, err
		}
		p, err := s.String("minio", pass)
		if err != nil {
			return nil, err
		}
		return credentials.NewStaticV4(u, p, ""), nil
	}
	switch {
	case name == "vault":
		c, err := creds("vault_writer_access_key", "vault_writer_secret_key")
		if err != nil {
			return nil, err
		}
		ca, err := s.String("tls", "ca_crt")
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(ca)) {
			return nil, errors.New("tls.ca_crt holds no certificate")
		}
		tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
		return minio.New(l.Env.VaultEndpoint, &minio.Options{Creds: c, Secure: true, Transport: tr})
	case strings.HasPrefix(name, "obj-"):
		c, err := creds("site_root_user", "site_root_password")
		if err != nil {
			return nil, err
		}
		return minio.New(name+":9000", &minio.Options{Creds: c})
	}
	return nil, fmt.Errorf("unknown attachment store %q (vault or obj-<site>)", name)
}

func (l *LabEnv) store(ctx context.Context, name string) (verify.ObjectLister, error) {
	c, err := l.minioClient(ctx, name)
	if err != nil {
		return nil, err
	}
	return verify.MinioLister{Client: c, Bucket: "attachments"}, nil
}

// CopyAttachments copies the current attachments from one store to another
// (vault or obj-<site>): filling a pilot-light site from the vault, or a
// returning site from the DR site before a switchover.
func (l *LabEnv) CopyAttachments(ctx context.Context, from, to string) (objcopy.Stats, error) {
	src, err := l.minioClient(ctx, from)
	if err != nil {
		return objcopy.Stats{}, err
	}
	dst, err := l.minioClient(ctx, to)
	if err != nil {
		return objcopy.Stats{}, err
	}
	return objcopy.Copy(ctx, objcopy.Store{Client: src, Bucket: "attachments"}, objcopy.Store{Client: dst, Bucket: "attachments"}, "documents/", 8)
}

// survivors returns the canary writes from seq `from` on that production holds.
func (l *LabEnv) survivors(ctx context.Context, from int64) (map[int64]bool, error) {
	seqs, err := l.canarySeqs(ctx, from)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(seqs))
	for _, s := range seqs {
		out[s] = true
	}
	return out, nil
}

func (l *LabEnv) canarySeqs(ctx context.Context, from int64) ([]int64, error) {
	url := fmt.Sprintf("%s/canary?from=%d", l.Env.PublicURL, from)
	code, body, err := l.get(ctx, url)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", url, code)
	}
	var resp struct {
		Seqs []int64 `json:"seqs"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return resp.Seqs, nil
}

// restorePointMargin keeps targets clear of a backup's end, the start of
// canary coverage and the archive's edge.
const restorePointMargin = 10 * time.Second

// PickRestorePoint chooses S6's random backup set and target (see restorepoint.Pick).
func (l *LabEnv) PickRestorePoint(ctx context.Context, repo int, seed int64) (restorepoint.Point, error) {
	info, err := l.backupInfo(ctx)
	if err != nil {
		return restorepoint.Point{}, err
	}
	backups, err := restorepoint.ParseInfo(info, repo)
	if err != nil {
		return restorepoint.Point{}, err
	}
	host, err := l.DBHost()
	if err != nil {
		return restorepoint.Point{}, err
	}
	rows, err := l.psql("postgres").Query(ctx, host, "SELECT coalesce(extract(epoch FROM last_archived_time), 0) FROM pg_stat_archiver")
	if err != nil {
		return restorepoint.Point{}, err
	}
	if len(rows) != 1 {
		return restorepoint.Point{}, fmt.Errorf("unexpected pg_stat_archiver result %v", rows)
	}
	archivedSecs, err := strconv.ParseFloat(rows[0][0], 64)
	if err != nil {
		return restorepoint.Point{}, err
	}
	archived := time.Unix(0, int64(archivedSecs*float64(time.Second))).UTC()

	entries, err := l.journal()
	if err != nil {
		return restorepoint.Point{}, err
	}
	if len(entries) == 0 {
		return restorepoint.Point{}, errors.New("canary journal is empty; is the canary service running?")
	}
	seqs, err := l.canarySeqs(ctx, entries[0].Seq)
	if err != nil {
		return restorepoint.Point{}, err
	}
	if len(seqs) == 0 {
		return restorepoint.Point{}, errors.New("production holds none of the journal's canary writes")
	}
	coverage, ok := canary.CoverageStart(entries, seqs[0])
	if !ok {
		return restorepoint.Point{}, errors.New("the canary journal does not cover production")
	}
	return restorepoint.Pick(backups, coverage, archived, restorePointMargin, seed)
}

// ProductionChecks verifies production as it is now: integrity, business
// rules and attachments in the active site's store.
func (l *LabEnv) ProductionChecks(ctx context.Context) ([]verify.Result, error) {
	inv, err := l.Env.Inventory()
	if err != nil {
		return nil, err
	}
	reg := l.Checks(RunContext{})
	p := verify.Params{Now: l.Env.Now(), With: map[string]string{"host": inv.DBHost(), "store": "obj-" + inv.ActiveSite}}
	var out []verify.Result
	for _, name := range []string{"amcheck", "business-rules", "db-object-consistency"} {
		res, err := reg[name].Run(ctx, p)
		if err != nil {
			return out, fmt.Errorf("%s: %w", name, err)
		}
		res.Name = name
		out = append(out, res)
	}
	return out, nil
}

// Reset returns the environment to a tier's baseline (TierBaselines; "" keeps
// the current one): it writes the topology, then runs Terraform (which also
// removes drill-only nodes such as the restore node) and the site playbook.
// It refuses while site-b serves, because only a failback (S7) may move the
// data back.
func (l *LabEnv) Reset(ctx context.Context, env, tier string, out io.Writer) error {
	if err := l.Safety(ctx, env); err != nil {
		return fmt.Errorf("safety lock: %w", err)
	}
	cur, err := lab.ReadTopology(l.Env.TopologyPath)
	if err != nil {
		return err
	}
	if cur.ActiveSite != "a" {
		return fmt.Errorf("site %s is active; fail back first (runbook s7-failback)", cur.ActiveSite)
	}
	if tier != "" {
		want, ok := TierBaselines[tier]
		if !ok {
			return fmt.Errorf("unknown tier %q", tier)
		}
		if err := lab.WriteTopology(l.Env.TopologyPath, want); err != nil {
			return err
		}
		fmt.Fprintf(out, "topology: %+v\n", want)
	}
	for _, args := range [][]string{
		{"terraform", "-chdir=" + l.Env.TerraformDir, "apply", "-input=false", "-auto-approve"},
		{"ansible-playbook", filepath.Join(l.Env.Root, "infra", "ansible", "playbooks", "site.yml")},
	} {
		fmt.Fprintf(out, "$ %s\n", strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = l.Env.Root, out, out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", args[0], err)
		}
	}
	return nil
}

// topology reads the intended topology (topology.auto.tfvars), the source
// the inventory is generated from.
func (l *LabEnv) topology() (string, string, error) {
	t, err := lab.ReadTopology(l.Env.TopologyPath)
	if err != nil {
		return "", "", err
	}
	return t.ActiveSite, t.StandbySite, nil
}

func (l *LabEnv) noFiringAlerts(ctx context.Context) (string, error) {
	active, err := activeAlerts(ctx, &http.Client{Timeout: 5 * time.Second}, l.Env.Alertmanager)
	if err != nil {
		return "", fmt.Errorf("alertmanager: %w", err)
	}
	if len(active) > 0 {
		return "", fmt.Errorf("firing: %s", strings.Join(active, ", "))
	}
	return "Alertmanager reports no active alert", nil
}

// TierBaselines are the topologies each DR tier starts from: pilot light has
// no site-b at all, warm standby a streaming replica in site-b.
var TierBaselines = map[string]lab.Topology{
	"pilot-light":  {ActiveSite: "a", SiteAEnabled: true},
	"warm-standby": {ActiveSite: "a", StandbySite: "b", SiteAEnabled: true, SiteBEnabled: true},
}
