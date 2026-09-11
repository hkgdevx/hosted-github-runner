package ghrctl

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Schema         int      `yaml:"schema"`
	Organization   string   `yaml:"organization"`
	Name           string   `yaml:"name"`
	Labels         []string `yaml:"labels"`
	Workdir        string   `yaml:"workdir"`
	CPUs           string   `yaml:"cpus"`
	Memory         string   `yaml:"memory"`
	ReservedCPUs   string   `yaml:"reserved_cpus"`
	ReservedMemory string   `yaml:"reserved_memory"`
	SharedMemory   string   `yaml:"shared_memory"`
	Image          string   `yaml:"image"`
	PreviousImage  string   `yaml:"previous_image,omitempty"`
}

var slug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
var runnerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var imageRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./_:@-]*$`)
var cpuValue = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
var memoryValue = regexp.MustCompile(`(?i)^[1-9][0-9]*([bkmg]|[kmgt]i?b)?$`)

func defaults(image string) Config {
	host, _ := os.Hostname()
	host = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(strings.ToLower(host), "-")
	host = strings.Trim(host, "-")
	if host == "" {
		host = "runner"
	}
	if len(host) > 48 {
		host = host[:48]
	}
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return Config{Schema: 1, Name: host + "-" + hex.EncodeToString(b), Labels: []string{"self-hosted", "linux", "x64", "development"}, Workdir: "_work", CPUs: "3", Memory: "6G", ReservedCPUs: "1", ReservedMemory: "2G", SharedMemory: "1G", Image: image}
}

func configDir(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		base = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("XDG_CONFIG_HOME must be absolute")
	}
	return filepath.Join(base, "ghrctl"), nil
}

func (c *Config) fillMissing(image string) {
	d := defaults(image)
	if c.Name == "" {
		c.Name = d.Name
	}
	if len(c.Labels) == 0 {
		c.Labels = d.Labels
	}
	if c.Workdir == "" {
		c.Workdir = d.Workdir
	}
	if c.Image == "" {
		c.Image = d.Image
	}
	if c.CPUs == "" {
		c.CPUs = d.CPUs
	}
	if c.Memory == "" {
		c.Memory = d.Memory
	}
	if c.ReservedCPUs == "" {
		c.ReservedCPUs = d.ReservedCPUs
	}
	if c.ReservedMemory == "" {
		c.ReservedMemory = d.ReservedMemory
	}
	if c.SharedMemory == "" {
		c.SharedMemory = d.SharedMemory
	}
}

// Check every existing path component, including parents, before reading or writing secrets.
func safePath(path string) error {
	abs, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	for p := abs; ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink path: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

func secureDir(dir string) error {
	if e := safePath(dir); e != nil {
		return e
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	return os.Chmod(dir, 0700)
}

func atomicWrite(path string, data []byte) error {
	if e := safePath(path); e != nil {
		return e
	}
	if e := secureDir(filepath.Dir(path)); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".ghrctl-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func readPrivate(path string) ([]byte, error) {
	if e := safePath(path); e != nil {
		return nil, e
	}
	st, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("expected regular file: %s", path)
	}
	if st.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("insecure permissions on %s; run chmod 600 on this file", path)
	}
	return os.ReadFile(path)
}

func load(dir string) (Config, string, error) {
	var c Config
	b, e := readPrivate(filepath.Join(dir, "config.yaml"))
	if e != nil {
		return c, "", e
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if e = d.Decode(&c); e != nil {
		return c, "", fmt.Errorf("malformed config.yaml: %w", e)
	}
	var extra any
	if e = d.Decode(&extra); e != io.EOF {
		return c, "", errors.New("config.yaml must contain one YAML document")
	}
	if c.Schema != 1 {
		return c, "", fmt.Errorf("unsupported configuration schema %d", c.Schema)
	}
	b, e = readPrivate(filepath.Join(dir, "credentials.env"))
	if os.IsNotExist(e) {
		return c, "", nil
	}
	if e != nil {
		return c, "", e
	}
	token, e := parseCredential(b)
	return c, token, e
}

func parseCredential(b []byte) (string, error) {
	s := strings.TrimSuffix(string(b), "\n")
	if !strings.HasPrefix(s, "ACCESS_TOKEN=") {
		return "", errors.New("malformed credentials.env; expected ACCESS_TOKEN=value")
	}
	t := strings.TrimPrefix(s, "ACCESS_TOKEN=")
	if strings.ContainsAny(t, "\r\n") {
		return "", errors.New("credentials must contain a single line")
	}
	return t, nil
}

func (c Config) validate(token string) error {
	if c.Schema != 1 {
		return errors.New("unsupported configuration schema")
	}
	if !slug.MatchString(c.Organization) {
		return errors.New("organization must be a GitHub organization slug")
	}
	if !runnerName.MatchString(c.Name) {
		return errors.New("runner name must be 1-64 letters, numbers, dots, underscores or hyphens")
	}
	if token == "" || strings.ContainsAny(token, "\r\n\x00'\" \\$") {
		return errors.New("a valid single-line PAT is required")
	}
	if !imageRef.MatchString(c.Image) {
		return errors.New("image is required; development builds need configure --image IMAGE")
	}
	if c.PreviousImage != "" && !imageRef.MatchString(c.PreviousImage) {
		return errors.New("invalid previous_image")
	}
	if c.Workdir == "" || strings.ContainsAny(c.Workdir, "\r\n\\$") || filepath.IsAbs(c.Workdir) || strings.Contains(c.Workdir, "..") {
		return errors.New("workdir must be a relative directory without traversal")
	}
	if !cpuValue.MatchString(c.CPUs) || !cpuValue.MatchString(c.ReservedCPUs) || !memoryValue.MatchString(c.Memory) || !memoryValue.MatchString(c.ReservedMemory) || !memoryValue.MatchString(c.SharedMemory) {
		return errors.New("invalid resource settings")
	}
	cpu, _ := strconv.ParseFloat(c.CPUs, 64)
	reserved, _ := strconv.ParseFloat(c.ReservedCPUs, 64)
	if cpu <= 0 || reserved <= 0 || reserved > cpu {
		return errors.New("CPU values must be positive; reservation cannot exceed limit")
	}
	for _, l := range c.Labels {
		if l == "" || strings.ContainsAny(l, ",\r\n\x00$") {
			return errors.New("invalid runner label")
		}
	}
	for _, required := range []string{"self-hosted", "linux", "x64"} {
		found := false
		for _, l := range c.Labels {
			if strings.EqualFold(l, required) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("missing required label %s", required)
		}
	}
	return nil
}

func save(dir string, c Config, token string) error {
	if e := c.validate(token); e != nil {
		return e
	}
	b, e := yaml.Marshal(c)
	if e != nil {
		return e
	}
	// Restore the original credential if the configuration commit fails.
	p := filepath.Join(dir, "credentials.env")
	old, oldErr := readPrivate(p)
	if oldErr != nil && !os.IsNotExist(oldErr) {
		return oldErr
	}
	if e = atomicWrite(p, []byte("ACCESS_TOKEN="+token+"\n")); e != nil {
		return e
	}
	if e = atomicWrite(filepath.Join(dir, "config.yaml"), b); e != nil {
		if oldErr == nil {
			_ = atomicWrite(p, old)
		} else {
			_ = os.Remove(p)
		}
		return e
	}
	return nil
}
