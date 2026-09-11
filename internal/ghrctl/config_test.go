package ghrctl

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func validConfig() Config {
	c := defaults("example/runner@sha256:" + strings.Repeat("a", 64))
	c.Organization = "test-org"
	return c
}

func TestConfigurationValidation(t *testing.T) {
	c := validConfig()
	if e := c.validate("test-token"); e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{"", "a\nb", "a\rb", "a'b", "a$b", "a b"} {
		if e := c.validate(token); e == nil {
			t.Fatalf("accepted invalid token %q", token)
		}
	}
	for _, change := range []func(*Config){func(c *Config) { c.Organization = "https://github.com/org" }, func(c *Config) { c.Name = "a/b" }, func(c *Config) { c.Image = "" }, func(c *Config) { c.Workdir = "../bad" }, func(c *Config) { c.Memory = "oops" }, func(c *Config) { c.Labels = []string{"custom"} }, func(c *Config) { c.Schema = 2 }} {
		c := validConfig()
		change(&c)
		if e := c.validate("token"); e == nil {
			t.Fatalf("accepted invalid config: %+v", c)
		}
	}
}

func TestPrivateConfigurationRoundTrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Unix permission semantics")
	}
	d := filepath.Join(t.TempDir(), "path with spaces")
	if _, _, e := load(d); !os.IsNotExist(e) {
		t.Fatalf("missing configuration: %v", e)
	}
	c := validConfig()
	if e := save(d, c, "secret"); e != nil {
		t.Fatal(e)
	}
	got, token, e := load(d)
	if e != nil || got.Name != c.Name || token != "secret" {
		t.Fatalf("round trip: %+v %v", got, e)
	}
	for path, mode := range map[string]os.FileMode{d: 0700, filepath.Join(d, "config.yaml"): 0600, filepath.Join(d, "credentials.env"): 0600} {
		st, e := os.Stat(path)
		if e != nil || st.Mode().Perm() != mode {
			t.Fatalf("permissions: %s %v", path, e)
		}
	}
	if e := save(d, c, "bad\ntoken"); e == nil {
		t.Fatal("expected rejection")
	}
	_, token, _ = load(d)
	if token != "secret" {
		t.Fatal("invalid update modified credentials")
	}
	if e := os.Chmod(filepath.Join(d, "credentials.env"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, _, e := load(d); e == nil {
		t.Fatal("accepted world-readable credentials")
	}
}

func TestMalformedAndUnsupportedConfiguration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Unix permission semantics")
	}
	d := t.TempDir()
	for _, body := range []string{"[bad", "schema: 999\n", "schema: 1\nunknown: true\n", "schema: 1\n---\nschema: 1\n"} {
		if e := atomicWrite(filepath.Join(d, "config.yaml"), []byte(body)); e != nil {
			t.Fatal(e)
		}
		if _, _, e := load(d); e == nil {
			t.Fatalf("accepted %q", body)
		}
	}
	if e := atomicWrite(filepath.Join(d, "config.yaml"), []byte("schema: 1\n")); e != nil {
		t.Fatal(e)
	}
	c, token, e := load(d)
	if e != nil || c.validate(token) == nil {
		t.Fatal("incomplete configuration should load but need setup")
	}
}

func TestUnsafePathsAndAtomicWrite(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Unix symlinks")
	}
	d := t.TempDir()
	target := filepath.Join(d, "target")
	if e := os.WriteFile(target, []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(d, "link")
	if e := os.Symlink(target, link); e != nil {
		t.Fatal(e)
	}
	if e := atomicWrite(link, []byte("changed")); e == nil {
		t.Fatal("followed symlink")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "original" {
		t.Fatal("target overwritten")
	}
	if e := atomicWrite(target, []byte("new")); e != nil {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(d)
	if len(entries) != 2 {
		t.Fatal("temporary file leaked")
	}
	parent := filepath.Join(d, "parent")
	if e := os.Symlink(d, parent); e != nil {
		t.Fatal(e)
	}
	if e := atomicWrite(filepath.Join(parent, "nested"), nil); e == nil {
		t.Fatal("followed parent symlink")
	}
}

func TestFailedConfigCommitRestoresCredential(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Unix private file semantics")
	}
	d := t.TempDir()
	c := validConfig()
	if e := atomicWrite(filepath.Join(d, "credentials.env"), []byte("ACCESS_TOKEN=original\n")); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(d, "config.yaml"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := save(d, c, "replacement"); e == nil {
		t.Fatal("expected commit failure")
	}
	b, e := readPrivate(filepath.Join(d, "credentials.env"))
	if e != nil || string(b) != "ACCESS_TOKEN=original\n" {
		t.Fatal("credential not restored", e)
	}
}

func TestConfigDirectoryAndFlags(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, e := configDir("")
	if e != nil || !strings.HasSuffix(d, "ghrctl") {
		t.Fatal(d, e)
	}
	cmd, o, e := parse([]string{"start", "--config-dir", "some path", "--timeout", "2s"})
	if e != nil || cmd != "start" || o.dir != "some path" {
		t.Fatal(cmd, o, e)
	}
	for _, args := range [][]string{{"invalid"}, {"start", "--timeout", "0s"}, {"--config-dir"}, {"configure", "--token", "secret"}, {"stop", "extra"}} {
		if _, _, e := parse(args); e == nil {
			t.Fatal("accepted", args)
		}
	}
	if got := strings.Join(normalizedLabels("LINUX,custom, custom,x64"), ","); got != "self-hosted,linux,x64,custom" {
		t.Fatal(got)
	}
}

func TestRedactionAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	r := redactor{out: &out, secrets: []string{"my-secret", "old-secret"}}
	for _, s := range []string{"hello my-", "secret and old-", "secret\ntrailing my-secret"} {
		if _, e := r.Write([]byte(s)); e != nil {
			t.Fatal(e)
		}
	}
	r.flush()
	if strings.Contains(out.String(), "secret") || strings.Count(out.String(), "[REDACTED]") != 3 {
		t.Fatal(out.String())
	}
}

func TestDockerEnvironmentIsolation(t *testing.T) {
	for _, key := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "COMPOSE_FILE", "COMPOSE_PROJECT_NAME", "COMPOSE_ENV_FILES", "ACCESS_TOKEN"} {
		t.Setenv(key, "unwanted")
	}
	env := strings.Join(dockerEnv(), "\n")
	if strings.Contains(env, "unwanted") {
		t.Fatal(env)
	}
	d := docker{dir: t.TempDir()}
	cmd := d.command(context.Background(), "info")
	if strings.Join(cmd.Args[1:], " ") != "--host unix:///var/run/docker.sock info" {
		t.Fatal(cmd.Args)
	}
}

func TestGitHubAPIValidationAndErrors(t *testing.T) {
	var mode string
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-GitHub-Api-Version") == "" {
			t.Error("missing headers")
		}
		if mode == "forbidden" {
			w.WriteHeader(403)
			fmt.Fprint(w, "token must not leak")
			return
		}
		if strings.HasSuffix(r.URL.Path, "registration-token") {
			if r.Method != "POST" {
				t.Error(r.Method)
			}
			fmt.Fprint(w, `{"token":"temporary"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/42") {
			fmt.Fprint(w, `{"id":42,"name":"runner","status":"online","busy":true}`)
			return
		}
		fmt.Fprint(w, `{"runners":[{"id":42,"name":"runner"}]}`)
	}))
	defer s.Close()
	g := github{s.URL, s.Client()}
	c := validConfig()
	c.Name = "runner"
	if e := g.validate(context.Background(), c, "token", 0); e == nil {
		t.Fatal("accepted name collision")
	}
	if e := g.validate(context.Background(), c, "token", 42); e != nil {
		t.Fatal(e)
	}
	r, e := g.runner(context.Background(), c, "token", 42)
	if e != nil || !r.Busy || r.Status != "online" {
		t.Fatal(r, e)
	}
	mode = "forbidden"
	e = g.validate(context.Background(), c, "token", 0)
	if e == nil || strings.Contains(e.Error(), "token must not leak") {
		t.Fatal(e)
	}
	if calls < 4 {
		t.Fatal(calls)
	}
}
