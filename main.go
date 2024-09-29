// Package main is the ISO generator wrapper
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

type (
	// Config handles input ISO build configurations
	Config struct {
		Tags       []string `yaml:"tag"`
		Repository struct {
			URL          string   `yaml:"url"`
			Repositories []string `yaml:"repositories"`
		} `yaml:"repository"`
		Architecture string `yaml:"architecture"`
		Name         string `yaml:"name"`
		Source       struct {
			Remote    string `yaml:"remote"`
			Directory string `yaml:"directory"`
			Template  string `yaml:"template"`
		} `yaml:"source"`
		Output    string              `yaml:"output"`
		Commands  map[string][]string `yaml:"commands"`
		Timestamp string              `yaml:"timestamp"`
	}
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	inConfig := flag.String("config", "", "configuration file")
	debug := flag.Bool("debug", false, "enable debugging")
	flag.Parse()
	b, err := os.ReadFile(*inConfig)
	if err != nil {
		return err
	}
	cfg := Config{}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return err
	}
	did := false
	isDebug := *debug
	for idx := range cfg.Tags {
		did = true
		if err := cfg.run(idx, isDebug); err != nil {
			return err
		}
	}
	if !did {
		return errors.New("no tags processed")
	}
	return nil
}

func (cfg Config) run(idx int, debug bool) error {
	dir, err := os.MkdirTemp("", "alpine-iso.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var tag string
	tag = cfg.Tags[idx]
	rawTag := tag
	if strings.Contains(rawTag, ".") {
		parts := strings.Split(tag, ".")
		if len(parts) != 3 {
			return fmt.Errorf("version must be X.Y.Z (have: %s)", parts)
		}
		for _, p := range parts {
			if _, err := strconv.Atoi(p); err != nil {
				return fmt.Errorf("invalid version, non-numeric? %v (%w)", parts, err)
			}
		}
		tag = fmt.Sprintf("v%s", strings.Join(parts[0:2], "."))
	} else {
		if rawTag != "edge" {
			return fmt.Errorf("unknown version/not edge: %s", rawTag)
		}
	}
	cmds := make(map[string][]string)
	if cfg.Commands != nil {
		for n, c := range cfg.Commands {
			var exe string
			var args []string
			switch len(c) {
			case 0:
				return fmt.Errorf("command has not executable settings")
			case 1:
			default:
				args = c[1:]
			}
			exe = c[0]
			t, err := exec.Command(exe, args...).Output()
			if err != nil {
				return err
			}
			cmds[n] = strings.Split(string(t), "\n")
		}
	}
	obj := struct {
		Name     string
		Arch     string
		Tag      string
		Commands map[string][]string
	}{cfg.Name, cfg.Architecture, tag, cmds}
	t, err := template.New("t").Parse(cfg.Source.Template)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, obj); err != nil {
		return err
	}
	if debug {
		fmt.Printf("profile: %s", buf.String())
	}
	var repositories []string
	url := cfg.Repository.URL
	for _, repo := range cfg.Repository.Repositories {
		r, err := template.New("t").Parse(fmt.Sprintf("%s/%s", url, repo))
		if err != nil {
			return err
		}
		var text bytes.Buffer
		if err := r.Execute(&text, obj); err != nil {
			return err
		}

		repositories = append(repositories, "--repository", text.String())
	}

	clone := filepath.Join(dir, "aports")
	cmd := exec.Command("git", "clone", "--depth=1", cfg.Source.Remote, clone)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return err
	}
	root := filepath.Join(clone, cfg.Source.Directory)
	profile := filepath.Join(root, fmt.Sprintf("mkimg.%s.sh", cfg.Name))
	if err := os.WriteFile(profile, buf.Bytes(), 0o755); err != nil {
		return err
	}

	out := filepath.Join(dir, "iso")
	if err := os.Mkdir(out, 0o755); err != nil {
		return err
	}
	args := []string{
		filepath.Join(root, "mkimage.sh"),
		"--outdir", out,
		"--arch", cfg.Architecture,
		"--profile", cfg.Name,
		"--tag", rawTag,
	}
	args = append(args, repositories...)
	if debug {
		fmt.Printf("mkimage arguments: %v\n", args)
	}
	cmd = exec.Command("sh", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	files, err := os.ReadDir(out)
	if err != nil {
		return nil
	}
	found := false
	home := os.Getenv("HOME")
	to := filepath.Join(home, cfg.Output)
	now := time.Now().Format(cfg.Timestamp)
	for _, f := range files {
		name := f.Name()
		if strings.HasSuffix(name, ".iso") {
			found = true
			dest := filepath.Join(to, fmt.Sprintf("%s.%s", now, name))
			fmt.Printf("artifact: %s (-> %s)\n", name, dest)
			if err := exec.Command("mv", filepath.Join(out, name), dest).Run(); err != nil {
				return err
			}
		}
	}
	if !found {
		return errors.New("no built iso found?")
	}
	return nil
}
