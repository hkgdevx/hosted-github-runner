package ghrctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

type Build struct{ Version, Commit, Image string }
type options struct {
	dir, org, name, labels, image               string
	noninteractive, tokenStdin, force, rollback bool
	timeout                                     time.Duration
}

const help = `ghrctl — persistent GitHub organization runner

Usage: ghrctl [--config-dir DIR] COMMAND [options]

Commands: start configure restart stop status logs pull doctor version help
          upgrade [--rollback]

configure: --org SLUG --name NAME --labels LABELS --image IMAGE
           --non-interactive --token-stdin
start/restart/upgrade: --timeout 3m
stop/restart/upgrade: --force (may interrupt an active job)

Run ghrctl start to set up your first runner. Docker Engine and Compose v2
must already be installed. Run ghrctl consistently as the same Linux user.
`

func parse(args []string) (string, options, error) {
	o := options{timeout: 3 * time.Minute}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--config-dir" {
			i++
			if i == len(args) {
				return "", o, errors.New("--config-dir requires a directory")
			}
			o.dir = args[i]
		} else if strings.HasPrefix(a, "--config-dir=") {
			o.dir = strings.TrimPrefix(a, "--config-dir=")
		} else {
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return "help", o, nil
	}
	cmd := rest[0]
	if cmd == "--help" || cmd == "-h" {
		cmd = "help"
	}
	if cmd == "--version" {
		cmd = "version"
	}
	f := flag.NewFlagSet(cmd, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	switch cmd {
	case "configure":
		f.StringVar(&o.org, "org", "", "organization")
		f.StringVar(&o.name, "name", "", "runner name")
		f.StringVar(&o.labels, "labels", "", "custom labels")
		f.StringVar(&o.image, "image", "", "runner image")
		f.BoolVar(&o.noninteractive, "non-interactive", false, "")
		f.BoolVar(&o.tokenStdin, "token-stdin", false, "")
	case "start":
		f.DurationVar(&o.timeout, "timeout", 3*time.Minute, "")
	case "restart", "upgrade":
		f.DurationVar(&o.timeout, "timeout", 3*time.Minute, "")
		f.BoolVar(&o.force, "force", false, "")
		if cmd == "upgrade" {
			f.BoolVar(&o.rollback, "rollback", false, "")
		}
	case "stop":
		f.BoolVar(&o.force, "force", false, "")
	case "status", "logs", "pull", "doctor", "version", "help":
	default:
		return "", o, fmt.Errorf("unknown command %q; run ghrctl help", cmd)
	}
	if e := f.Parse(rest[1:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return "help", o, nil
		}
		return "", o, e
	}
	if f.NArg() != 0 {
		return "", o, errors.New("unexpected positional arguments")
	}
	if o.timeout <= 0 {
		return "", o, errors.New("--timeout must be positive")
	}
	return cmd, o, nil
}

func Run(args []string, b Build) error {
	cmd, o, e := parse(args)
	if e != nil {
		return e
	}
	if cmd == "version" {
		fmt.Printf("ghrctl %s\ncommit: %s\nbundled image: %s\n", b.Version, b.Commit, b.Image)
		return nil
	}
	dir, e := configDir(o.dir)
	if e != nil {
		return e
	}
	if cmd == "help" {
		fmt.Print(help)
		_, _, e := load(dir)
		switch {
		case os.IsNotExist(e):
			fmt.Println("Configuration: not configured")
		case e != nil:
			fmt.Println("Configuration:", e)
		default:
			fmt.Println("Configuration:", dir)
		}
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	mutating := cmd == "configure" || cmd == "start" || cmd == "restart" || cmd == "stop" || cmd == "upgrade" || cmd == "pull"
	if mutating {
		unlock, e := lock(dir)
		if e != nil {
			return e
		}
		defer unlock()
	}
	c, token, e := load(dir)
	missing := os.IsNotExist(e)
	if e != nil && !missing {
		return e
	}
	if missing {
		c = defaults(b.Image)
	}
	a := app{dir: dir, build: b, gh: newGitHub(), out: os.Stdout}
	redactOut := &redactor{out: os.Stdout}
	redactErr := &redactor{out: os.Stderr}
	defer redactOut.flush()
	defer redactErr.flush()
	a.d = docker{dir: dir, out: redactOut, errout: redactErr}
	redactOut.secrets = []string{token}
	redactErr.secrets = []string{token}
	if cmd == "configure" || cmd == "start" && (missing || c.validate(token) != nil) {
		if cmd == "start" {
			fmt.Println("Configuration missing or incomplete. Let's set up your runner.")
		}
		c, token, e = a.configure(ctx, c, token, o)
		if e != nil {
			return e
		}
		redactOut.secrets = append(redactOut.secrets, token)
		redactErr.secrets = append(redactErr.secrets, token)
		if cmd == "configure" {
			return nil
		}
	} else if missing {
		return fmt.Errorf("no configuration at %s; run ghrctl start or ghrctl configure", dir)
	}
	if e = c.validate(token); e != nil {
		return e
	}
	if e = a.d.preflight(ctx); e != nil {
		return e
	}
	con, e := a.d.managed(ctx)
	if e != nil {
		return e
	}
	if con != nil {
		_, old, e := a.d.applied()
		if e != nil {
			return e
		}
		redactOut.secrets = append(redactOut.secrets, old)
		redactErr.secrets = append(redactErr.secrets, old)
	}
	switch cmd {
	case "doctor":
		own := int64(0)
		if con != nil {
			applied, _, e := a.d.applied()
			if e != nil {
				return e
			}
			if strings.EqualFold(applied.Organization, c.Organization) {
				own, _ = a.d.runnerID(ctx)
			}
		}
		if e = a.gh.validate(ctx, c, token, own); e != nil {
			return e
		}
		fmt.Println("OK: configuration, permissions, Linux x64, local Docker, Compose v2 and GitHub runner permissions.")
		return a.d.legacy(ctx)
	case "status":
		return a.status(ctx, c, token, con)
	case "logs":
		if con == nil {
			return errors.New("runner container does not exist; run ghrctl start")
		}
		e = a.d.compose(ctx, "logs", "--follow", "--tail=200", "github-runner")
		if ctx.Err() != nil {
			return nil
		}
		return e
	case "pull":
		return a.d.run(ctx, "pull", c.Image)
	case "stop":
		return a.stop(ctx, c, token, con, o.force)
	case "start":
		return a.start(ctx, c, token, con, o.timeout)
	case "restart", "upgrade":
		return a.recreate(ctx, cmd, c, token, con, o)
	}
	return nil
}

type app struct {
	dir   string
	build Build
	gh    github
	d     docker
	out   io.Writer
}

func normalizedLabels(value string) []string {
	labels := []string{"self-hosted", "linux", "x64"}
	seen := map[string]bool{"self-hosted": true, "linux": true, "x64": true}
	for _, l := range strings.Split(value, ",") {
		l = strings.TrimSpace(l)
		if l != "" && !seen[strings.ToLower(l)] {
			labels = append(labels, l)
			seen[strings.ToLower(l)] = true
		}
	}
	return labels
}

func (a app) configure(ctx context.Context, c Config, t string, o options) (Config, string, error) {
	c.fillMissing(a.build.Image)
	if o.org != "" {
		c.Organization = o.org
	}
	if o.name != "" {
		c.Name = o.name
	}
	if o.labels != "" {
		c.Labels = normalizedLabels(o.labels)
	}
	if o.image != "" {
		c.Image = o.image
	}
	if o.noninteractive {
		if o.org == "" || o.name == "" || o.labels == "" || !o.tokenStdin {
			return c, t, errors.New("noninteractive setup requires --org, --name, --labels and --token-stdin")
		}
	} else if !term.IsTerminal(int(os.Stdin.Fd())) {
		return c, t, errors.New("interactive setup requires a terminal; use configure --non-interactive --org ORG --name NAME --labels LABELS --token-stdin")
	}
	if o.tokenStdin {
		data, e := io.ReadAll(io.LimitReader(os.Stdin, 8193))
		if e != nil {
			return c, t, e
		}
		if len(data) > 8192 {
			return c, t, errors.New("token input too long")
		}
		t = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	}
	if !o.noninteractive {
		prompt := func(label, old string) (string, error) {
			fmt.Fprintf(a.out, "%s [%s]: ", label, old)
			s, e := readInput(ctx, os.Stdin, false)
			if e != nil {
				return "", errors.New("setup cancelled; configuration was not saved")
			}
			s = strings.TrimSpace(s)
			if s == "" {
				s = old
			}
			return s, nil
		}
		var e error
		if c.Organization, e = prompt("GitHub organization", c.Organization); e != nil {
			return c, t, e
		}
		if !o.tokenStdin {
			fmt.Fprint(a.out, "GitHub access token (hidden; Enter keeps existing): ")
			data, e := readInput(ctx, os.Stdin, true)
			fmt.Fprintln(a.out)
			if e != nil {
				return c, t, errors.New("token entry cancelled; configuration was not saved")
			}
			if len(data) > 0 {
				t = data
			}
		}
		if c.Name, e = prompt("Runner name", c.Name); e != nil {
			return c, t, e
		}
		labels, e := prompt("Runner labels", strings.Join(c.Labels, ","))
		if e != nil {
			return c, t, e
		}
		c.Labels = normalizedLabels(labels)
	}
	if e := ctx.Err(); e != nil {
		return c, t, e
	}
	if e := c.validate(t); e != nil {
		return c, t, e
	}
	if e := a.d.preflight(ctx); e != nil {
		return c, t, e
	}
	if e := a.d.validateCompose(ctx, c, t); e != nil {
		return c, t, e
	}
	con, e := a.d.managed(ctx)
	if e != nil {
		return c, t, e
	}
	var own int64
	if con != nil {
		old, _, e := a.d.applied()
		if e != nil {
			return c, t, e
		}
		if strings.EqualFold(old.Organization, c.Organization) {
			own, _ = a.d.runnerID(ctx)
		}
	}
	if e = a.gh.validate(ctx, c, t, own); e != nil {
		return c, t, e
	}
	if e = ctx.Err(); e != nil {
		return c, t, e
	}
	if e = save(a.dir, c, t); e != nil {
		return c, t, e
	}
	fmt.Fprintln(a.out, "Configuration saved in", a.dir)
	if con != nil {
		fmt.Fprintln(a.out, "Run ghrctl restart to apply the new settings after active jobs finish.")
	}
	return c, t, nil
}

func (a app) identity(ctx context.Context, c Config, t string) (Config, string, int64, error) {
	applied, old, e := a.d.applied()
	if e != nil {
		return c, t, 0, e
	}
	if !strings.EqualFold(c.Organization, applied.Organization) {
		t = old
	}
	id, e := a.d.runnerID(ctx)
	return applied, t, id, e
}

func (a app) busy(ctx context.Context, c Config, t string, con *container, force bool) error {
	if con == nil {
		return nil
	}
	if force {
		fmt.Fprintln(a.out, "Forcing operation; an active job may be interrupted.")
		return nil
	}
	applied, token, id, e := a.identity(ctx, c, t)
	if e == nil {
		var r Runner
		r, e = a.gh.runner(ctx, applied, token, id)
		if e == nil && r.Busy {
			return errors.New("runner is busy; wait for jobs to finish or use --force")
		}
	}
	if e != nil {
		return fmt.Errorf("cannot verify whether runner is busy: %w; use --force only if interruption is acceptable", e)
	}
	fmt.Fprintln(a.out, "Runner is not busy. This check does not prevent a new job from being assigned.")
	return nil
}

func (a app) stop(ctx context.Context, c Config, t string, con *container, force bool) error {
	if con == nil {
		fmt.Fprintln(a.out, "Runner is already stopped.")
		return nil
	}
	if e := a.busy(ctx, c, t, con, force); e != nil {
		return e
	}
	applied, token, id, idErr := a.identity(ctx, c, t)
	if e := a.d.compose(ctx, "down"); e != nil {
		return e
	}
	fmt.Fprintln(a.out, "Runner container removed; configuration and caches retained.")
	if idErr != nil {
		fmt.Fprintln(a.out, "Warning: could not identify the registration; check GitHub for a stale runner.")
		return nil
	}
	_, e := a.gh.runner(ctx, applied, token, id)
	var api apiError
	if errors.As(e, &api) && api.Status == 404 {
		fmt.Fprintln(a.out, "GitHub registration removed.")
		return nil
	}
	if e != nil {
		fmt.Fprintln(a.out, "Warning: deregistration could not be verified:", e)
	} else {
		fmt.Fprintln(a.out, "Warning: runner registration remains in GitHub; remove it only after confirming it is stale.")
	}
	return nil
}

func (a app) start(ctx context.Context, c Config, t string, con *container, timeout time.Duration) error {
	if e := a.d.legacy(ctx); e != nil {
		return e
	}
	gid, e := socketGID(a.d.socketPath())
	if e != nil {
		return e
	}
	if con != nil {
		match, e := a.d.matches(c, t, gid)
		if e != nil {
			return e
		}
		if !match {
			return errors.New("saved settings differ from the applied deployment; run ghrctl restart")
		}
		if !con.State.Running {
			if e = a.d.compose(ctx, "start", "github-runner"); e != nil {
				return e
			}
		}
	} else {
		if e = a.gh.validate(ctx, c, t, 0); e != nil {
			return e
		}
		if e = a.d.run(ctx, "pull", c.Image); e != nil {
			return e
		}
		if e = a.d.apply(c, t, gid); e != nil {
			return e
		}
		if e = a.d.compose(ctx, "config", "--quiet"); e != nil {
			return e
		}
		if e = a.d.compose(ctx, "up", "-d"); e != nil {
			return e
		}
	}
	return a.ready(ctx, c, t, timeout)
}

func (a app) ready(ctx context.Context, c Config, t string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fmt.Fprintln(a.out, "Waiting for GitHub runner registration...")
	var last error
	for {
		con, e := a.d.managed(ctx)
		if e == nil && con != nil && con.State.Running {
			var id int64
			id, e = a.d.runnerID(ctx)
			if e == nil {
				var r Runner
				r, e = a.gh.runner(ctx, c, t, id)
				if e == nil && r.Status == "online" {
					fmt.Fprintf(a.out, "Runner %s is online (busy=%t).\n", r.Name, r.Busy)
					return nil
				}
			}
		}
		if e != nil {
			last = e
			var api apiError
			if errors.As(e, &api) && (api.Status == 401 || api.Status == 403) {
				return fmt.Errorf("readiness unknown: %w; container may still be running; inspect ghrctl status and ghrctl logs", e)
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("runner readiness not confirmed: %v (last check: %v); container may still be running; inspect ghrctl status and ghrctl logs", ctx.Err(), last)
		case <-time.After(2 * time.Second):
		}
	}
}

func (a app) status(ctx context.Context, c Config, t string, con *container) error {
	if con == nil {
		fmt.Fprintf(a.out, "Container: absent\nConfigured runner: %s/%s\nSelected image: %s\nGitHub: unknown (no local registration ID)\n", c.Organization, c.Name, c.Image)
		return nil
	}
	fmt.Fprintf(a.out, "Container: %s\nImage: %s\n", con.State.Status, con.Config.Image)
	applied, token, id, e := a.identity(ctx, c, t)
	fmt.Fprintf(a.out, "Runner: %s/%s\n", applied.Organization, applied.Name)
	if e == nil {
		var r Runner
		r, e = a.gh.runner(ctx, applied, token, id)
		if e == nil {
			fmt.Fprintf(a.out, "GitHub: %s (busy=%t, ID=%d)\n", r.Status, r.Busy, r.ID)
			return nil
		}
	}
	fmt.Fprintln(a.out, "GitHub: unknown")
	return e
}

// Buffer complete lines so secrets split across subprocess writes are redacted.
type redactor struct {
	out     io.Writer
	secrets []string
	pending string
}

func (r *redactor) Write(p []byte) (int, error) {
	r.pending += string(p)
	for {
		line, rest, ok := strings.Cut(r.pending, "\n")
		if !ok {
			break
		}
		r.pending = rest
		if _, e := io.WriteString(r.out, r.clean(line)+"\n"); e != nil {
			return 0, e
		}
	}
	return len(p), nil
}
func (r *redactor) clean(s string) string {
	for _, secret := range r.secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
	}
	return s
}
func (r *redactor) flush() { _, _ = io.WriteString(r.out, r.clean(r.pending)); r.pending = "" }
