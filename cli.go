package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"strings"

	"ai-gateway-metering-proxy/internal/cliproxy"
	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/pricing"
)

// run is the testable application entrypoint. main only maps the final error
// to process exit behavior.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd, rest, err := splitCommand(args)
	if err != nil {
		return err
	}

	switch cmd {
	case "", "serve":
		return runServe(rest, stdout, stderr)
	case "validate":
		return runValidate(rest, stdout, stderr)
	case "hash-key":
		return runHashKey(rest, stdin, stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func splitCommand(args []string) (cmd string, rest []string, err error) {
	if len(args) == 0 {
		return "", nil, nil
	}
	if strings.HasPrefix(args[0], "-") {
		// Legacy flag-only invocation remains the default serve path.
		return "", args, nil
	}
	cmd = args[0]
	rest = args[1:]
	switch cmd {
	case "serve", "validate", "hash-key":
		return cmd, rest, nil
	default:
		return "", nil, fmt.Errorf("unknown command %q", cmd)
	}
}

func newConfigFlagSet(name string, errorHandling flag.ErrorHandling) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, errorHandling)
	configPath := fs.String("config", "config.yaml", "path to config file")
	return fs, configPath
}

func runValidate(args []string, stdout, stderr io.Writer) error {
	fs, configPath := newConfigFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("validate: unexpected arguments")
	}

	cfg, err := config.LoadStrict(*configPath)
	if err != nil {
		return fmt.Errorf("validate: config: %w", err)
	}

	if _, err := pricing.Load(cfg.PricingFile); err != nil {
		return fmt.Errorf("validate: pricing: %w", err)
	}

	if err := hash.ValidateSaltFile(cfg.SaltFile); err != nil {
		// Never surface salt bytes in error text.
		return fmt.Errorf("validate: salt: %w", err)
	}

	if cfg.CLIProxyManagement.Enabled {
		key, err := cliproxy.ReadKeyFile(cfg.CLIProxyManagement.KeyFile)
		if err != nil {
			return fmt.Errorf("validate: management key file is not readable")
		}
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("validate: management key file is empty")
		}
	}

	if _, err := fmt.Fprintln(stdout, "ok"); err != nil {
		return err
	}
	return nil
}

func runHashKey(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs, configPath := newConfigFlagSet("hash-key", flag.ContinueOnError)
	fs.SetOutput(stderr)
	// Explicitly reject --key / -key so process lists and shell history never see plaintext.
	var rejectedKey string
	fs.StringVar(&rejectedKey, "key", "", "unsupported; pass the key on stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	keyFlagPresent := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "key" {
			keyFlagPresent = true
		}
	})
	if keyFlagPresent {
		return fmt.Errorf("hash-key: --key is not supported; provide the plaintext key on stdin")
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("hash-key: unexpected arguments; provide the plaintext key on stdin only")
	}

	cfg, err := config.LoadStrict(*configPath)
	if err != nil {
		return fmt.Errorf("hash-key: config: %w", err)
	}

	hasher, err := hash.LoadValidated(cfg.SaltFile)
	if err != nil {
		return fmt.Errorf("hash-key: salt: %w", err)
	}

	key, err := readSinglePlaintextLine(stdin)
	if err != nil {
		return err
	}

	// stdout contains only the hash plus newline; never log plaintext, length, or prefix.
	if _, err := fmt.Fprintln(stdout, hasher.Hash(key)); err != nil {
		return err
	}
	return nil
}

// readSinglePlaintextLine accepts one stdin line, strips a single terminal CRLF/LF,
// and rejects empty input or unexpected additional plaintext lines.
func readSinglePlaintextLine(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("hash-key: failed to read key from stdin")
	}
	if len(line) == 0 && err == io.EOF {
		return "", fmt.Errorf("hash-key: empty key")
	}

	// Strip only the terminal newline sequence from this one line.
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		return "", fmt.Errorf("hash-key: empty key")
	}

	// Reject any bytes after the first newline, including blank lines. Accepting
	// them would make the documented one-line stdin contract ambiguous.
	rest, readErr := io.ReadAll(br)
	if readErr != nil {
		return "", fmt.Errorf("hash-key: failed to read key from stdin")
	}
	if len(rest) != 0 {
		return "", fmt.Errorf("hash-key: unexpected additional input")
	}
	return line, nil
}

// serveFlags holds the legacy/serve process flags.
type serveFlags struct {
	configPath string
	devStatic  bool
	seedDemo   bool
}

func parseServeFlags(args []string, stderr io.Writer) (serveFlags, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "config.yaml", "path to config file")
	devStatic := fs.Bool("dev-static", false, "serve WebUI static files from disk (internal/webui/static/) for local development")
	seedDemo := fs.Bool("seed-demo", false, "insert demo data into the database for local WebUI testing")
	if err := fs.Parse(args); err != nil {
		return serveFlags{}, err
	}
	if fs.NArg() > 0 {
		return serveFlags{}, fmt.Errorf("serve: unexpected arguments")
	}
	return serveFlags{
		configPath: *configPath,
		devStatic:  *devStatic,
		seedDemo:   *seedDemo,
	}, nil
}
