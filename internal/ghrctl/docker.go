package ghrctl

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/template"
)

//go:embed compose.yaml.tmpl
var composeTemplate string

type docker struct {
	dir    string
	out    io.Writer
	errout io.Writer
	socket string // Empty in production; Unix listener fixture in tests.
}

func (d docker) socketPath() string {
	if d.socket != "" {
		return d.socket
	}
	return "/var/run/docker.sock"
}

// Whitelist host environment needed for executable lookup and registry credentials.
// Never inherit Compose overrides, Docker contexts, or the caller's .env.
func dockerEnv() []string {
	var result []string
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "TMPDIR", "TMP", "TEMP", "LANG", "SSH_AUTH_SOCK", "DOCKER_CONFIG"} {
		if v, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+v)
		}
	}
	return append(result, "COMPOSE_DISABLE_ENV_FILE=1")
}

func (d docker) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...)
	cmd.Env = dockerEnv()
	cmd.Dir = d.dir
	return cmd
}

func (d docker) capture(ctx context.Context, args ...string) ([]byte, error) {
	cmd := d.command(ctx, args...)
	b, e := cmd.Output()
	if e != nil {
		return nil, fmt.Errorf("docker %s failed; check Docker permissions and daemon availability", args[0])
	}
	return b, nil
}

func (d docker) run(ctx context.Context, args ...string) error {
	cmd := d.command(ctx, args...)
	cmd.Stdout = d.out
	cmd.Stderr = d.errout
	if e := cmd.Run(); e != nil {
		return fmt.Errorf("docker %s failed: %w", args[0], e)
	}
	return nil
}

func (d docker) preflight(ctx context.Context) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return errors.New("supported host: Linux x64 with Docker Engine and Compose v2")
	}
	if _, e := socketGID(d.socketPath()); e != nil {
		return e
	}
	if _, e := d.capture(ctx, "info"); e != nil {
		return e
	}
	b, e := d.capture(ctx, "compose", "version", "--short")
	if e != nil {
		return e
	}
	if !strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(string(b)), "v"), "2.") {
		return errors.New("Docker Compose v2 is required")
	}
	return nil
}

type container struct {
	ID     string `json:"Id"`
	Config struct {
		Image  string
		Labels map[string]string
	}
	State struct {
		Running bool
		Status  string
	}
}

func (d docker) inspect(ctx context.Context, name string) (*container, error) {
	// Listing first distinguishes a missing container from a daemon/permission failure.
	b, e := d.capture(ctx, "container", "ls", "-a", "--filter", "name=^/"+name+"$", "--format", "{{.ID}}")
	if e != nil {
		return nil, e
	}
	if strings.TrimSpace(string(b)) == "" {
		return nil, nil
	}
	b, e = d.capture(ctx, "container", "inspect", name)
	if e != nil {
		return nil, e
	}
	var list []container
	if e = json.Unmarshal(b, &list); e != nil {
		return nil, e
	}
	if len(list) != 1 {
		return nil, errors.New("unexpected Docker inspect response")
	}
	return &list[0], nil
}

func (d docker) owner() string {
	sum := sha256.Sum256([]byte(d.dir))
	return hex.EncodeToString(sum[:])
}

func (d docker) managed(ctx context.Context) (*container, error) {
	// Compose identifies services by project labels, not just container_name.
	names, e := d.capture(ctx, "container", "ls", "-a", "--filter", "label=com.docker.compose.project=ghrctl", "--format", "{{.Names}}")
	if e != nil {
		return nil, e
	}
	for _, name := range strings.Fields(string(names)) {
		if name != "ghrctl-runner" {
			return nil, fmt.Errorf("Compose project ghrctl contains unexpected container %s; refusing to modify it", name)
		}
	}
	c, e := d.inspect(ctx, "ghrctl-runner")
	if e != nil || c == nil {
		return c, e
	}
	if c.Config.Labels["io.ghrctl.owner"] != d.owner() || c.Config.Labels["com.docker.compose.project"] != "ghrctl" || c.Config.Labels["com.docker.compose.service"] != "github-runner" {
		return nil, errors.New("ghrctl-runner belongs to another deployment; refusing to adopt or modify it")
	}
	return c, nil
}

func (d docker) validateCompose(ctx context.Context, c Config, t string) error {
	gid, e := socketGID(d.socketPath())
	if e != nil {
		return e
	}
	b, env, e := d.render(c, t, gid)
	if e != nil {
		return e
	}
	tmp, e := os.MkdirTemp(d.dir, ".validate-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	if e = atomicWrite(filepath.Join(tmp, "compose.yaml"), b); e != nil {
		return e
	}
	if e = atomicWrite(filepath.Join(tmp, "runner.env"), env); e != nil {
		return e
	}
	_, e = d.capture(ctx, "compose", "--project-name", "ghrctl", "--project-directory", tmp, "--env-file", "/dev/null", "-f", filepath.Join(tmp, "compose.yaml"), "config", "--quiet")
	if e != nil {
		return errors.New("Compose rejected the runner configuration; check resource values and Compose v2 installation")
	}
	return nil
}

func (d docker) legacy(ctx context.Context) error {
	c, e := d.inspect(ctx, "github-runner")
	if e != nil {
		return e
	}
	if c != nil {
		return errors.New("legacy github-runner container found; stop/remove it with the old Bash helper before migration; old cache volumes will be retained")
	}
	return nil
}

func (d docker) runnerID(ctx context.Context) (int64, error) {
	b, e := d.capture(ctx, "exec", "ghrctl-runner", "cat", "/home/gthb-runner/actions-runner/.runner")
	if e != nil {
		return 0, e
	}
	var r struct {
		ID int64 `json:"agentId"`
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return 0, e
	}
	if r.ID <= 0 {
		return 0, errors.New("runner registration is not available yet")
	}
	return r.ID, nil
}

func (d docker) render(c Config, t string, gid uint32) ([]byte, []byte, error) {
	quote := func(s string) string { return strconv.Quote(strings.ReplaceAll(s, "$", "$$")) }
	tmpl, e := template.New("compose").Funcs(template.FuncMap{"quote": quote}).Parse(composeTemplate)
	if e != nil {
		return nil, nil, e
	}
	var b bytes.Buffer
	e = tmpl.Execute(&b, struct {
		Config Config
		Owner  string
		GID    uint32
	}{c, d.owner(), gid})
	if e != nil {
		return nil, nil, e
	}
	values := [][2]string{{"ORG_NAME", c.Organization}, {"ACCESS_TOKEN", t}, {"RUNNER_NAME", c.Name}, {"RUNNER_LABELS", strings.Join(c.Labels, ",")}, {"RUNNER_WORKDIR", c.Workdir}}
	var env strings.Builder
	for _, kv := range values {
		fmt.Fprintf(&env, "%s='%s'\n", kv[0], strings.ReplaceAll(strings.ReplaceAll(kv[1], `\`, `\\`), "'", `\'`))
	}
	return b.Bytes(), []byte(env.String()), nil
}

func (d docker) apply(c Config, t string, gid uint32) error {
	b, env, e := d.render(c, t, gid)
	if e != nil {
		return e
	}
	r := filepath.Join(d.dir, "runtime")
	if e = atomicWrite(filepath.Join(r, "runner.env"), env); e != nil {
		return e
	}
	return atomicWrite(filepath.Join(r, "compose.yaml"), b)
}

func (d docker) matches(c Config, t string, gid uint32) (bool, error) {
	b, env, e := d.render(c, t, gid)
	if e != nil {
		return false, e
	}
	old, e := readPrivate(filepath.Join(d.dir, "runtime", "compose.yaml"))
	if e != nil {
		return false, e
	}
	oldEnv, e := readPrivate(filepath.Join(d.dir, "runtime", "runner.env"))
	if e != nil {
		return false, e
	}
	return bytes.Equal(b, old) && bytes.Equal(env, oldEnv), nil
}

func (d docker) compose(ctx context.Context, args ...string) error {
	r := filepath.Join(d.dir, "runtime")
	for _, name := range []string{"compose.yaml", "runner.env"} {
		if _, e := readPrivate(filepath.Join(r, name)); e != nil {
			return e
		}
	}
	base := []string{"compose", "--project-name", "ghrctl", "--project-directory", r, "--env-file", "/dev/null", "-f", filepath.Join(r, "compose.yaml")}
	return d.run(ctx, append(base, args...)...)
}

// Read the applied identity and token, which may differ after configure/rotation.
func (d docker) applied() (Config, string, error) {
	b, e := readPrivate(filepath.Join(d.dir, "runtime", "runner.env"))
	if e != nil {
		return Config{}, "", e
	}
	v := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
			return Config{}, "", errors.New("invalid applied runner environment")
		}
		value = value[1 : len(value)-1]
		value = strings.ReplaceAll(value, `\'`, "'")
		value = strings.ReplaceAll(value, `\\`, `\`)
		v[key] = value
	}
	return Config{Organization: v["ORG_NAME"], Name: v["RUNNER_NAME"]}, v["ACCESS_TOKEN"], nil
}
