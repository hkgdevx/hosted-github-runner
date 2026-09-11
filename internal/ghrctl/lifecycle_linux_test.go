//go:build linux

package ghrctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withStdin(t *testing.T, value string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "input")
	if e := os.WriteFile(p, []byte(value), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; f.Close() })
}

func TestNoninteractiveSetupAndFailedValidation(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(fmt.Sprint(fails), func(t *testing.T) {
			f := newFixture(t)
			withStdin(t, "new-token\n")
			if fails {
				f.mark(t, "api-fails")
			}
			o := options{noninteractive: true, tokenStdin: true, org: "test-org", name: "runner", labels: "development", image: f.c.Image}
			_, _, e := f.a.configure(context.Background(), Config{Schema: 1}, "", o)
			if fails {
				if e == nil {
					t.Fatal("accepted failed validation")
				}
				if _, e := os.Stat(filepath.Join(f.dir, "config.yaml")); !os.IsNotExist(e) {
					t.Fatal("saved invalid configuration")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			c, token, e := load(f.dir)
			if e != nil || token != "new-token" || c.Workdir != "_work" {
				t.Fatal(c, e)
			}
			if strings.Contains(f.out.String(), "new-token") {
				t.Fatal("token exposed")
			}
		})
	}
}

func TestNoninteractiveMissingAndMultilineToken(t *testing.T) {
	f := newFixture(t)
	withStdin(t, "token\ninjected=value\n")
	if _, _, e := f.a.configure(context.Background(), f.c, "", options{noninteractive: true}); e == nil {
		t.Fatal("accepted missing flags")
	}
	o := options{noninteractive: true, tokenStdin: true, org: "test-org", name: "runner", labels: "development"}
	if _, _, e := f.a.configure(context.Background(), f.c, "", o); e == nil {
		t.Fatal("accepted multiline token")
	}
	if _, e := os.Stat(filepath.Join(f.dir, "config.yaml")); !os.IsNotExist(e) {
		t.Fatal("saved invalid token")
	}
}

func TestUpgradeRollbackAndFailure(t *testing.T) {
	for _, failure := range []string{"", "pull-fails", "offline"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			if e := save(f.dir, f.c, "token"); e != nil {
				t.Fatal(e)
			}
			if e := f.a.start(ctx, f.c, "token", nil, time.Second); e != nil {
				t.Fatal(e)
			}
			con, _ := f.a.d.managed(ctx)
			old := f.c.Image
			target := "example/runner@sha256:" + strings.Repeat("b", 64)
			f.a.build.Image = target
			if failure != "" {
				f.mark(t, failure)
			}
			before := f.calls()
			e := f.a.recreate(ctx, "upgrade", f.c, "token", con, options{timeout: 100 * time.Millisecond})
			selected, _, loadErr := load(f.dir)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if failure == "pull-fails" {
				if e == nil || selected.Image != old || strings.Contains(strings.TrimPrefix(f.calls(), before), `"down"`) {
					t.Fatal("failed pull changed deployment", e)
				}
				return
			}
			if selected.Image != target || selected.PreviousImage != old {
				t.Fatal(selected)
			}
			if failure == "offline" {
				if e == nil || !strings.Contains(e.Error(), "--rollback") {
					t.Fatal(e)
				}
				os.Remove(filepath.Join(f.dir, "offline"))
			} else if e != nil {
				t.Fatal(e)
			}
			con, _ = f.a.d.managed(ctx)
			if e = f.a.recreate(ctx, "upgrade", selected, "token", con, options{rollback: true, timeout: time.Second}); e != nil {
				t.Fatal(e)
			}
			selected, _, _ = load(f.dir)
			if selected.Image != old || selected.PreviousImage != target {
				t.Fatal(selected)
			}
		})
	}
}

func TestRestartKeepsSelectedImage(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.a.build.Image = "example/runner:new"
	if e := save(f.dir, f.c, "token"); e != nil {
		t.Fatal(e)
	}
	if e := f.a.start(ctx, f.c, "token", nil, time.Second); e != nil {
		t.Fatal(e)
	}
	con, _ := f.a.d.managed(ctx)
	if e := f.a.recreate(ctx, "restart", f.c, "token", con, options{timeout: time.Second}); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(f.calls(), "example/runner:new") {
		t.Fatal("restart upgraded implicitly")
	}
}

func TestTerminalPromptHelper(t *testing.T) {
	if os.Getenv("GHRCTL_PROMPT_HELPER") != "1" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if os.Getenv("GHRCTL_CANCEL_PROMPT") == "1" {
		time.AfterFunc(150*time.Millisecond, cancel)
	}
	fmt.Println("READY")
	s, e := readInput(ctx, os.Stdin, true)
	if os.Getenv("GHRCTL_CANCEL_PROMPT") == "1" {
		if e == nil {
			t.Fatal("expected cancellation")
		}
		fmt.Println("CANCELLED")
		return
	}
	if e != nil || s != "hidden-token" {
		t.Fatal("unexpected password result", e)
	}
	fmt.Println("ACCEPTED")
}

func TestHiddenTerminalInputAndCancellation(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip("python3 PTY harness required")
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	script := `
import os,pty,subprocess,sys,select,time,termios
for cancel in ('0','1'):
    master,slave=pty.openpty()
    before=termios.tcgetattr(slave)
    env=dict(os.environ,GHRCTL_PROMPT_HELPER='1',GHRCTL_CANCEL_PROMPT=cancel)
    child=subprocess.Popen([sys.argv[1],'-test.run=^TestTerminalPromptHelper$'],stdin=slave,stdout=slave,stderr=slave,env=env)
    data=b''
    deadline=time.monotonic()+5
    sent=False
    while child.poll() is None and time.monotonic()<deadline:
        if select.select([master],[],[],0.02)[0]: data+=os.read(master,4096)
        if b'READY' in data and not sent and cancel=='0' and not termios.tcgetattr(slave)[3]&termios.ECHO:
            os.write(master,b'hidden-token\n'); sent=True
    if child.poll() is None: child.kill(); raise RuntimeError('prompt hung')
    while select.select([master],[],[],0)[0]: data+=os.read(master,4096)
    assert child.returncode==0,data
    assert b'hidden-token' not in data,data
    assert (b'CANCELLED' if cancel=='1' else b'ACCEPTED') in data,data
    assert termios.tcgetattr(slave)==before,'terminal settings not restored'
    os.close(master);os.close(slave)
`
	cmd := exec.Command("python3", "-c", script, exe)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("PTY test: %v\n%s", e, b)
	}
}

type fixture struct {
	a   app
	c   Config
	out *bytes.Buffer
	dir string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip("python3 needed for fake Docker")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if e := os.Mkdir(bin, 0700); e != nil {
		t.Fatal(e)
	}
	root, _ := json.Marshal(dir)
	script := `#!/usr/bin/env python3
import sys,json,pathlib
p=pathlib.Path(ROOT)
a=sys.argv[3:]
with (p/'calls').open('a') as f: f.write(json.dumps(a)+'\n')
def marker(n): return (p/n).exists()
if a==['info']: print('{}')
elif a==['compose','version','--short']: print('2.24.6')
elif a[:2]==['container','ls']:
    if 'label=com.docker.compose.project=ghrctl' in a and marker('foreign-project'): print('other-container')
    if 'name=^/ghrctl-runner$' in a and marker('container'): print('id')
    if 'name=^/github-runner$' in a and marker('legacy'): print('old-id')
elif a[:2]==['container','inspect']:
    print((p/'container').read_text())
elif a[0]=='exec':
    if marker('no-id'): sys.exit(1)
    print('{"agentId":42}')
elif a[0]=='pull':
    if marker('pull-fails'): sys.exit(1)
elif a[0]=='compose':
    if 'down' in a: (p/'container').unlink(missing_ok=True)
    elif 'up' in a or 'start' in a:
        (p/'container').write_text((p/'next-container').read_text())
else: sys.exit(12)
`
	script = strings.Replace(script, "ROOT", string(root), 1)
	if e := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	l, e := net.Listen("unix", filepath.Join(dir, "socket"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close() })
	out := &bytes.Buffer{}
	d := docker{dir: dir, out: out, errout: out, socket: filepath.Join(dir, "socket")}
	c := validConfig()
	c.Name = "runner"
	con := container{ID: "id"}
	con.Config.Image = c.Image
	con.Config.Labels = map[string]string{"io.ghrctl.owner": d.owner(), "com.docker.compose.project": "ghrctl", "com.docker.compose.service": "github-runner"}
	con.State.Running = true
	con.State.Status = "running"
	b, _ := json.Marshal([]container{con})
	if e = os.WriteFile(filepath.Join(dir, "next-container"), b, 0600); e != nil {
		t.Fatal(e)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, e := os.Stat(filepath.Join(dir, "api-fails")); e == nil {
			w.WriteHeader(403)
			return
		}
		if strings.HasSuffix(r.URL.Path, "registration-token") {
			fmt.Fprint(w, `{"token":"temporary"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/42") {
			if _, e := os.Stat(filepath.Join(dir, "container")); e != nil {
				w.WriteHeader(404)
				return
			}
			status := "online"
			if _, e := os.Stat(filepath.Join(dir, "offline")); e == nil {
				status = "offline"
			}
			_, e := os.Stat(filepath.Join(dir, "busy"))
			fmt.Fprintf(w, `{"id":42,"name":"runner","status":%q,"busy":%t}`, status, e == nil)
			return
		}
		fmt.Fprint(w, `{"runners":[]}`)
	}))
	t.Cleanup(s.Close)
	return &fixture{app{dir: dir, d: d, gh: github{s.URL, s.Client()}, out: out}, c, out, dir}
}

func (f *fixture) mark(t *testing.T, name string) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(f.dir, name), nil, 0600); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) calls() string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return string(b)
}

func TestStartReadinessIdempotenceAndStop(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if e := f.a.d.preflight(ctx); e != nil {
		t.Fatal(e)
	}
	if e := f.a.start(ctx, f.c, "token", nil, time.Second); e != nil {
		t.Fatal(e)
	}
	con, e := f.a.d.managed(ctx)
	if e != nil || con == nil {
		t.Fatal(e)
	}
	before := f.calls()
	if e = f.a.start(ctx, f.c, "token", con, time.Second); e != nil {
		t.Fatal(e)
	}
	after := strings.TrimPrefix(f.calls(), before)
	if strings.Contains(after, `"up"`) || strings.Contains(after, `"pull"`) {
		t.Fatal("start recreated existing container", after)
	}
	f.c.Labels = append(f.c.Labels, "changed")
	if e = f.a.start(ctx, f.c, "token", con, time.Second); e == nil || !strings.Contains(e.Error(), "restart") {
		t.Fatal(e)
	}
	if e = f.a.stop(ctx, f.c, "token", con, false); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(f.calls(), `"-v"`) {
		t.Fatal("deleted cache volumes")
	}
	if !strings.Contains(f.out.String(), "registration removed") {
		t.Fatal(f.out.String())
	}
}

func TestBusyAndUnknownGuard(t *testing.T) {
	for _, marker := range []string{"busy", "api-fails", "no-id"} {
		t.Run(marker, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			if e := f.a.start(ctx, f.c, "token", nil, time.Second); e != nil {
				t.Fatal(e)
			}
			con, _ := f.a.d.managed(ctx)
			f.mark(t, marker)
			if e := f.a.stop(ctx, f.c, "token", con, false); e == nil {
				t.Fatal("unguarded stop")
			}
			if strings.Contains(f.calls(), `"down"`) {
				t.Fatal("stopped before guard")
			}
			if e := f.a.stop(ctx, f.c, "token", con, true); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestReadinessTimeoutAndAuthenticationFailure(t *testing.T) {
	for _, marker := range []string{"offline", "api-fails", "no-id"} {
		t.Run(marker, func(t *testing.T) {
			f := newFixture(t)
			if e := f.a.start(context.Background(), f.c, "token", nil, time.Second); e != nil {
				t.Fatal(e)
			}
			f.mark(t, marker)
			e := f.a.ready(context.Background(), f.c, "token", 100*time.Millisecond)
			if e == nil || !strings.Contains(e.Error(), "may still be running") {
				t.Fatal(e)
			}
			if _, e = os.Stat(filepath.Join(f.dir, "container")); e != nil {
				t.Fatal("removed failed deployment")
			}
		})
	}
}

func TestOwnershipLegacyAndPullFailures(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.mark(t, "foreign-project")
	if _, e := f.a.d.managed(ctx); e == nil {
		t.Fatal("accepted unrelated Compose project container")
	}
	os.Remove(filepath.Join(f.dir, "foreign-project"))
	f.mark(t, "legacy")
	if e := f.a.start(ctx, f.c, "token", nil, time.Second); e == nil {
		t.Fatal("accepted legacy container")
	}
	os.Remove(filepath.Join(f.dir, "legacy"))
	f.mark(t, "pull-fails")
	if e := f.a.start(ctx, f.c, "token", nil, time.Second); e == nil {
		t.Fatal("ignored pull failure")
	}
	if strings.Contains(f.calls(), `"up"`) {
		t.Fatal("started after pull failure")
	}
	os.Remove(filepath.Join(f.dir, "pull-fails"))
	if e := f.a.start(ctx, f.c, "token", nil, time.Second); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(f.dir, "container"))
	b = bytes.ReplaceAll(b, []byte(f.a.d.owner()), []byte("other"))
	os.WriteFile(filepath.Join(f.dir, "container"), b, 0600)
	if _, e := f.a.d.managed(ctx); e == nil {
		t.Fatal("adopted unrelated container")
	}
}

func TestCancelledSetupPreservesConfiguration(t *testing.T) {
	f := newFixture(t)
	if e := save(f.dir, f.c, "original-token"); e != nil {
		t.Fatal(e)
	}
	withStdin(t, "new-token\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := options{noninteractive: true, tokenStdin: true, org: "different-org", name: "new-runner", labels: "development"}
	if _, _, e := f.a.configure(ctx, f.c, "original-token", o); e == nil {
		t.Fatal("ignored cancellation")
	}
	c, token, e := load(f.dir)
	if e != nil || c.Organization != f.c.Organization || token != "original-token" {
		t.Fatal("cancel changed saved state", e)
	}
}

func TestAppliedConfigurationSurvivesConfigure(t *testing.T) {
	f := newFixture(t)
	if e := f.a.start(context.Background(), f.c, "old-token", nil, time.Second); e != nil {
		t.Fatal(e)
	}
	f.c.Organization = "new-org"
	if e := save(f.dir, f.c, "new-token"); e != nil {
		t.Fatal(e)
	}
	old, token, e := f.a.d.applied()
	if e != nil || old.Organization != "test-org" || token != "old-token" {
		t.Fatal(old, e)
	}
}

func TestConcurrentLockAndRelease(t *testing.T) {
	d := t.TempDir()
	unlock, e := lock(d)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := lock(d); e == nil {
		other()
		t.Fatal("allowed concurrent mutation")
	}
	unlock()
	again, e := lock(d)
	if e != nil {
		t.Fatal(e)
	}
	again()
}

func TestComposeRendering(t *testing.T) {
	if os.Getenv("GHRCTL_COMPOSE_TEST") != "1" {
		t.Skip("set GHRCTL_COMPOSE_TEST=1 to validate using real Compose")
	}
	d := docker{dir: t.TempDir(), out: os.Stdout, errout: os.Stderr}
	c := validConfig()
	c.Labels = append(c.Labels, "quote'label", "space label")
	if e := d.apply(c, "dummy-token", 123); e != nil {
		t.Fatal(e)
	}
	r := filepath.Join(d.dir, "runtime")
	args := []string{"--project-name", "ghrctl", "--project-directory", r, "--env-file", "/dev/null", "-f", filepath.Join(r, "compose.yaml"), "config", "--format", "json"}
	var cmd *exec.Cmd
	if binary := os.Getenv("GHRCTL_COMPOSE_BINARY"); binary != "" {
		cmd = exec.Command(binary, args...)
		cmd.Env = dockerEnv()
	} else {
		cmd = d.command(context.Background(), append([]string{"compose"}, args...)...)
	}
	b, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	var rendered struct {
		Services map[string]struct {
			Environment map[string]string
			GroupAdd    []string `json:"group_add"`
		}
	}
	if e = json.Unmarshal(b, &rendered); e != nil {
		t.Fatal(e)
	}
	s := rendered.Services["github-runner"]
	if s.Environment["ACCESS_TOKEN"] != "dummy-token" || s.Environment["RUNNER_LABELS"] != strings.Join(c.Labels, ",") || len(s.GroupAdd) != 1 || s.GroupAdd[0] != "123" {
		t.Fatal("Compose changed environment values or GID")
	}
}
