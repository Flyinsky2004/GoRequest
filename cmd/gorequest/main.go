// Command gorequest is the standalone CLI for GoRequest. It shares the pure-Go
// core with the desktop app and never starts a GUI.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

const (
	exitOK       = 0
	exitRun      = 1
	exitConfig   = 2
	exitInternal = 3
	exitCanceled = 130

	version = "0.1.0-mvp"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitConfig
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Printf("gorequest %s (core format %d)\n", version, core.SupportedSchemaVersion)
		return exitOK
	case "validate":
		return cmdValidate(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "help", "-h", "--help":
		usage()
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		return exitConfig
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `GoRequest CLI (MVP)

Usage:
  gorequest version
  gorequest validate   --project DIR [--env ID] [--json]
  gorequest run        --project DIR --request ID [--env ID] [--secret name=env:VAR] [--timeout 10s] [--json]
  gorequest doctor     [--project DIR]

Exit codes: 0 ok, 1 run failure, 2 config error, 3 internal, 130 canceled.
`)
}

// stringList collects repeatable flags.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func openProject(path string) (*core.Project, int) {
	p, err := core.OpenProject(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, exitConfig
	}
	return p, exitOK
}

func printIssues(issues []core.Issue) {
	for _, i := range issues {
		fmt.Fprintln(os.Stderr, i.String())
	}
}

func cmdValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", ".", "project directory")
	env := fs.String("env", "", "environment id (informational)")
	jsonOut := fs.Bool("json", false, "emit JSON")
	_ = fs.Bool("non-interactive", false, "no prompts")
	verbose := fs.Bool("verbose", false, "verbose output")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	p, code := openProject(*project)
	if p == nil {
		return code
	}
	if *env != "" {
		if _, ok := p.Environments[*env]; !ok {
			fmt.Fprintf(os.Stderr, "error: environment %q not found\n", *env)
			return exitConfig
		}
	}
	issues := p.Issues
	if *jsonOut {
		out := map[string]any{
			"project":  p.File.Name,
			"requests": len(p.Requests),
			"errors":   core.ErrorCount(issues),
			"issues":   issues,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	} else {
		printIssues(issues)
		fmt.Printf("project %q: %d request(s), %d error(s)\n", p.File.Name, len(p.Requests), core.ErrorCount(issues))
		if *verbose {
			names := make([]string, 0, len(p.Requests))
			for id := range p.Requests {
				names = append(names, id)
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Printf("  - %s  %s\n", n, p.Requests[n].RelPath)
			}
		}
	}
	if core.ErrorCount(issues) > 0 {
		return exitConfig
	}
	return exitOK
}

type multiSecrets struct{ providers []core.SecretProvider }

func (m multiSecrets) Get(name string) (string, bool) {
	for _, p := range m.providers {
		if v, ok := p.Get(name); ok {
			return v, true
		}
	}
	return "", false
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", ".", "project directory")
	requestID := fs.String("request", "", "request id to run")
	env := fs.String("env", "", "environment id")
	timeout := fs.String("timeout", "", "override timeout, e.g. 10s")
	jsonOut := fs.Bool("json", false, "emit JSON")
	var secrets stringList
	fs.Var(&secrets, "secret", "bind secret: name=env:VAR (repeatable)")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if *requestID == "" {
		fmt.Fprintln(os.Stderr, "error: --request is required")
		return exitConfig
	}
	p, code := openProject(*project)
	if p == nil {
		return code
	}
	if core.ErrorCount(p.Issues) > 0 {
		printIssues(p.Issues)
		fmt.Fprintln(os.Stderr, "error: project has validation errors; refusing to run")
		return exitConfig
	}
	entry, ok := p.Requests[*requestID]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: request %q not found\n", *requestID)
		return exitConfig
	}

	explicit := core.MapSecrets{}
	var bindNames []string
	for _, spec := range secrets {
		name, ref, ok := strings.Cut(spec, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "error: bad --secret %q (want name=env:VAR)\n", spec)
			return exitConfig
		}
		varName, isEnv := strings.CutPrefix(ref, "env:")
		if !isEnv {
			fmt.Fprintf(os.Stderr, "error: secret %q must reference env:VAR\n", name)
			return exitConfig
		}
		v, present := os.LookupEnv(varName)
		if !present || v == "" {
			fmt.Fprintf(os.Stderr, "error: secret %q: environment variable %s is empty or unset\n", name, varName)
			return exitConfig
		}
		explicit[name] = v
		bindNames = append(bindNames, name)
	}
	provider := multiSecrets{providers: []core.SecretProvider{explicit, platform.NewEnvSecrets()}}

	vars, environment, err := p.ResolveVars(entry, *env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitConfig
	}
	if environment != nil {
		// Environment secrets are declarations; only secrets actually
		// referenced by this request are required, and BuildPlan below reports
		// any that are missing. Collect bound values for redaction.
		for _, sn := range environment.Secrets {
			if _, ok := provider.Get(sn); ok {
				bindNames = append(bindNames, sn)
			}
		}
	}

	req := entry.Request
	if *timeout != "" {
		clone := *req
		clone.Timeout = *timeout
		req = &clone
	}
	authScheme, err := p.AuthFor(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitConfig
	}
	plan, err := core.BuildPlan(req, vars, provider, authScheme)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitConfig
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := core.Execute(ctx, plan, nil)
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "canceled")
			return exitCanceled
		}
		if core.IsRun(err) {
			fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
			return exitRun
		}
		fmt.Fprintf(os.Stderr, "internal error: %v\n", err)
		return exitInternal
	}

	redact := collectSecretValues(provider, bindNames)
	if *jsonOut {
		printJSON(res, redact)
	} else {
		printHuman(res, redact)
	}
	return exitOK
}

func collectSecretValues(provider core.SecretProvider, names []string) []string {
	var vals []string
	seen := map[string]bool{}
	for _, n := range names {
		if v, ok := provider.Get(n); ok && v != "" && !seen[v] {
			seen[v] = true
			vals = append(vals, v)
		}
	}
	return vals
}

func redactString(s string, secrets []string) string {
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, "***")
	}
	return s
}

func printHuman(res *core.ExecResult, secrets []string) {
	fmt.Printf("%s %d · %s · %d B", res.Proto, res.Status, res.Duration.Round(time.Millisecond), res.BodySize)
	if res.Truncated {
		fmt.Print(" (truncated)")
	}
	fmt.Println()
	for _, h := range res.Headers {
		if strings.EqualFold(h[0], "Authorization") || strings.EqualFold(h[0], "Set-Cookie") {
			fmt.Printf("%s: ***\n", h[0])
			continue
		}
		fmt.Printf("%s: %s\n", h[0], redactString(h[1], secrets))
	}
	fmt.Println()
	body := redactString(string(res.Body), secrets)
	if res.Pretty != "" {
		body = redactString(res.Pretty, secrets)
	}
	fmt.Println(body)
}

func printJSON(res *core.ExecResult, secrets []string) {
	headers := make([][2]string, 0, len(res.Headers))
	for _, h := range res.Headers {
		v := h[1]
		if strings.EqualFold(h[0], "Authorization") || strings.EqualFold(h[0], "Set-Cookie") {
			v = "***"
		}
		headers = append(headers, [2]string{h[0], redactString(v, secrets)})
	}
	body := string(res.Body)
	if res.Pretty != "" {
		body = res.Pretty
	}
	out := map[string]any{
		"status":     res.Status,
		"proto":      res.Proto,
		"durationMs": float64(res.Duration) / float64(time.Millisecond),
		"size":       res.BodySize,
		"truncated":  res.Truncated,
		"headers":    headers,
		"body":       redactString(body, secrets),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", "", "optional project directory")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	fmt.Printf("gorequest %s\n", version)
	fmt.Printf("go: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Printf("core schema support: %d\n", core.SupportedSchemaVersion)
	if *project == "" {
		fmt.Println("project: (none given)")
		return exitOK
	}
	p, err := core.OpenProject(*project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitConfig
	}
	fmt.Printf("project: %s (%s)\n", p.File.Name, p.Dir)
	fmt.Printf("requests: %d, environments: %d, auth schemes: %d\n", len(p.Requests), len(p.Environments), len(p.AuthSchemes))
	envs := make([]string, 0, len(p.Environments))
	for id := range p.Environments {
		envs = append(envs, id)
	}
	sort.Strings(envs)
	provider := platform.NewEnvSecrets()
	for _, id := range envs {
		env := p.Environments[id]
		var missing []string
		for _, s := range env.Secrets {
			if _, ok := provider.Get(s); !ok {
				missing = append(missing, s)
			}
		}
		if len(missing) > 0 {
			fmt.Printf("env %s: secrets not bound: %s\n", id, strings.Join(missing, ", "))
		} else if len(env.Secrets) > 0 {
			fmt.Printf("env %s: all %d secret(s) bound\n", id, len(env.Secrets))
		} else {
			fmt.Printf("env %s: no secrets declared\n", id)
		}
	}
	if core.ErrorCount(p.Issues) > 0 {
		printIssues(p.Issues)
		return exitConfig
	}
	fmt.Println("project validation: OK")
	return exitOK
}
