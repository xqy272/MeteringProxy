package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ai-gateway-metering-proxy/internal/hash"
)

func writeSaltFile(t *testing.T, dir, contents string) string {
	t.Helper()
	path := filepath.Join(dir, "salt")
	mode := os.FileMode(0600)
	if runtime.GOOS == "windows" {
		mode = 0644
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("write salt: %v", err)
	}
	return path
}

func writePricingFile(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "pricing.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("write pricing: %v", err)
	}
	return path
}

func writeCLIConfig(t *testing.T, dir string, extra string) (configPath, dbPath, saltPath, pricingPath string) {
	t.Helper()
	dbPath = filepath.Join(dir, "usage.sqlite")
	saltPath = writeSaltFile(t, dir, "test-salt-bytes\n")
	pricingBody := "pricing:\n  test-model:\n    input_per_1m: 1.0\n    output_per_1m: 2.0\n"
	pricingPath = writePricingFile(t, dir, pricingBody)
	configPath = filepath.Join(dir, "config.yaml")
	body := "listen: \"127.0.0.1:0\"\n" +
		"upstream: \"http://127.0.0.1:8317\"\n" +
		"database: \"" + filepath.ToSlash(dbPath) + "\"\n" +
		"salt_file: \"" + filepath.ToSlash(saltPath) + "\"\n" +
		"pricing_file: \"" + filepath.ToSlash(pricingPath) + "\"\n" +
		"webui:\n  enabled: false\n" +
		extra
	if err := os.WriteFile(configPath, []byte(body), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath, dbPath, saltPath, pricingPath
}

func TestSplitCommandLegacyAndExplicit(t *testing.T) {
	cmd, rest, err := splitCommand([]string{"--config", "x.yaml"})
	if err != nil || cmd != "" || len(rest) != 2 {
		t.Fatalf("legacy: cmd=%q rest=%v err=%v", cmd, rest, err)
	}
	cmd, rest, err = splitCommand([]string{"serve", "--config", "x.yaml"})
	if err != nil || cmd != "serve" || len(rest) != 2 {
		t.Fatalf("serve: cmd=%q rest=%v err=%v", cmd, rest, err)
	}
	cmd, rest, err = splitCommand([]string{"validate", "--config", "x.yaml"})
	if err != nil || cmd != "validate" {
		t.Fatalf("validate: cmd=%q err=%v", cmd, err)
	}
	if _, _, err := splitCommand([]string{"doctor"}); err == nil {
		t.Fatal("expected unknown command error")
	}
}

func TestParseServeFlagsLegacy(t *testing.T) {
	var stderr bytes.Buffer
	flags, err := parseServeFlags([]string{"--config", "cfg.yaml", "--dev-static", "--seed-demo"}, &stderr)
	if err != nil {
		t.Fatalf("parseServeFlags: %v", err)
	}
	if flags.configPath != "cfg.yaml" || !flags.devStatic || !flags.seedDemo {
		t.Fatalf("flags = %+v", flags)
	}
}

func TestRunValidateSuccessDoesNotCreateDBOrTouchNetwork(t *testing.T) {
	dir := t.TempDir()
	configPath, dbPath, _, _ := writeCLIConfig(t, dir, "")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"validate", "--config", configPath}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("validate: %v\nstderr=%s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "ok" {
		t.Fatalf("stdout = %q, want ok", stdout.String())
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("validate must not create/open sqlite path %s (err=%v)", dbPath, err)
	}
}

func TestRunValidateMissingSalt(t *testing.T) {
	dir := t.TempDir()
	configPath, _, saltPath, _ := writeCLIConfig(t, dir, "")
	if err := os.Remove(saltPath); err != nil {
		t.Fatalf("remove salt: %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"validate", "--config", configPath}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected missing salt error")
	}
	msg := err.Error()
	if strings.Contains(msg, "test-salt-bytes") {
		t.Fatalf("error leaked salt bytes: %s", msg)
	}
	if !strings.Contains(msg, "salt") {
		t.Fatalf("error = %v, want salt failure", err)
	}
}

func TestRunValidateMissingPricing(t *testing.T) {
	dir := t.TempDir()
	configPath, _, _, pricingPath := writeCLIConfig(t, dir, "")
	if err := os.Remove(pricingPath); err != nil {
		t.Fatalf("remove pricing: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"validate", "--config", configPath}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("expected missing pricing error")
	}
}

func TestRunValidateUnknownPricingField(t *testing.T) {
	dir := t.TempDir()
	configPath, _, _, pricingPath := writeCLIConfig(t, dir, "")
	if err := os.WriteFile(pricingPath, []byte("pricing: {}\nunknown_top_level: true\n"), 0600); err != nil {
		t.Fatalf("write pricing: %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"validate", "--config", configPath}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected unknown pricing field error")
	}
	if !strings.Contains(err.Error(), "pricing") {
		t.Fatalf("error = %v, want pricing path", err)
	}
}

func TestRunValidateKeyLabelError(t *testing.T) {
	dir := t.TempDir()
	configPath, _, _, _ := writeCLIConfig(t, dir, "key_labels:\n  \"not-a-hash\": \"friend\"\n")
	var stdout, stderr bytes.Buffer
	err := run([]string{"validate", "--config", configPath}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected key label error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "key_labels") {
		t.Fatalf("error = %v, want key_labels", err)
	}
}

func TestRunValidateMissingManagementKeyWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	missingKey := filepath.ToSlash(filepath.Join(dir, "missing-mgmt.key"))
	extra := "cliproxy_management:\n  enabled: true\n  base_url: \"http://127.0.0.1:8317/v0/management\"\n  key_file: \"" + missingKey + "\"\n"
	configPath, _, _, _ := writeCLIConfig(t, dir, extra)
	var stdout, stderr bytes.Buffer
	err := run([]string{"validate", "--config", configPath}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected management key failure")
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("leaked secret material: %v", err)
	}
}

func TestRunValidateManagementKeyReadable(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "mgmt.key")
	if err := os.WriteFile(keyPath, []byte("super-secret-mgmt-key\n"), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	extra := "cliproxy_management:\n  enabled: true\n  base_url: \"http://127.0.0.1:8317/v0/management\"\n  key_file: \"" + filepath.ToSlash(keyPath) + "\"\n"
	configPath, dbPath, _, _ := writeCLIConfig(t, dir, extra)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"validate", "--config", configPath}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if strings.Contains(stdout.String(), "super-secret") || strings.Contains(stderr.String(), "super-secret") {
		t.Fatal("management key leaked to stdout/stderr")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("validate must not create DB when management is enabled")
	}
}

func TestRunHashKeyEquivalenceAndCRLF(t *testing.T) {
	dir := t.TempDir()
	configPath, _, saltPath, _ := writeCLIConfig(t, dir, "")
	hasher, err := hash.New(saltPath)
	if err != nil {
		t.Fatalf("hash.New: %v", err)
	}
	want := hasher.Hash("sk-test-key")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"hash-key", "--config", configPath}, strings.NewReader("sk-test-key\r\n"), &stdout, &stderr); err != nil {
		t.Fatalf("hash-key: %v stderr=%s", err, stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	if got != want {
		t.Fatalf("hash = %q, want %q", got, want)
	}
	if stdout.String() != want+"\n" {
		t.Fatalf("stdout must be hash plus newline only, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "sk-test") || strings.Contains(stderr.String(), "sk-test") {
		t.Fatal("plaintext leaked")
	}
	if _, err := os.Stat(filepath.Join(dir, "usage.sqlite")); !os.IsNotExist(err) {
		t.Fatal("hash-key must not create DB")
	}
}

func TestRunHashKeyRejectsEmptyMultilineAndKeyFlag(t *testing.T) {
	dir := t.TempDir()
	configPath, _, _, _ := writeCLIConfig(t, dir, "")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"hash-key", "--config", configPath}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("expected empty key error")
	}
	if err := run([]string{"hash-key", "--config", configPath}, strings.NewReader("line1\nline2\n"), &stdout, &stderr); err == nil {
		t.Fatal("expected multiline rejection")
	}
	if err := run([]string{"hash-key", "--config", configPath}, strings.NewReader("line1\n\n"), &stdout, &stderr); err == nil {
		t.Fatal("expected trailing blank-line rejection")
	}
	err := run([]string{"hash-key", "--config", configPath, "--key", "sk-secret"}, strings.NewReader("sk-secret\n"), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected --key rejection")
	}
	if strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("error leaked plaintext key: %v", err)
	}
	err = run([]string{"hash-key", "--config", configPath, "--key="}, strings.NewReader("sk-secret\n"), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected empty --key= rejection")
	}
	err = run([]string{"hash-key", "--config", configPath, "sk-positional"}, strings.NewReader("sk-positional\n"), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected positional rejection")
	}
	if strings.Contains(err.Error(), "sk-positional") {
		t.Fatalf("error leaked positional key: %v", err)
	}
}

func TestProductionCommandsUseStrictConfig(t *testing.T) {
	dir := t.TempDir()
	configPath, _, _, _ := writeCLIConfig(t, dir, "not_a_real_field: true\n")
	var stdout, stderr bytes.Buffer

	if err := run([]string{"hash-key", "--config", configPath}, strings.NewReader("sk-test\n"), &stdout, &stderr); err == nil {
		t.Fatal("hash-key accepted an unknown config field")
	}
	if err := run([]string{"serve", "--config", configPath}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("serve accepted an unknown config field")
	}
}

func TestMergeKeyLabelsPreservesExplicitConfiguration(t *testing.T) {
	configured := map[string]string{"a": "operator-label"}
	defaults := map[string]string{"a": "demo-label", "b": "demo-only"}
	merged := mergeKeyLabels(configured, defaults)

	if merged["a"] != "operator-label" || merged["b"] != "demo-only" {
		t.Fatalf("merged labels = %#v", merged)
	}
	merged["a"] = "changed"
	delete(merged, "b")
	if configured["a"] != "operator-label" || defaults["a"] != "demo-label" || defaults["b"] != "demo-only" {
		t.Fatal("mergeKeyLabels mutated an input map")
	}
}

func TestRunUnknownCommandAndInvalidFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"not-a-command"}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("expected unknown command")
	}
	if err := run([]string{"validate", "--nope"}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("expected invalid flag error")
	}
}

func TestRunValidateEmptySalt(t *testing.T) {
	dir := t.TempDir()
	configPath, _, saltPath, _ := writeCLIConfig(t, dir, "")
	if err := os.WriteFile(saltPath, []byte(""), 0600); err != nil {
		t.Fatalf("truncate salt: %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"validate", "--config", configPath}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected empty salt failure")
	}
	if strings.Contains(err.Error(), "test-salt-bytes") {
		t.Fatalf("leaked salt bytes: %v", err)
	}
	if !strings.Contains(err.Error(), "salt") {
		t.Fatalf("error = %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	err = run([]string{"serve", "--config", configPath}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("serve accepted an empty salt")
	}
	if strings.Contains(err.Error(), "test-salt-bytes") {
		t.Fatalf("serve leaked salt bytes: %v", err)
	}
}

func TestRunValidateUnknownConfigFieldAndMultiDoc(t *testing.T) {
	dir := t.TempDir()
	configPath, _, _, _ := writeCLIConfig(t, dir, "not_a_real_field: true\n")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"validate", "--config", configPath}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("expected unknown config field rejection")
	}

	// Rewrite config with multi-doc
	configPath, _, _, _ = writeCLIConfig(t, dir, "")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if err := os.WriteFile(configPath, append(raw, []byte("---\nlisten: \"127.0.0.1:1\"\n")...), 0600); err != nil {
		t.Fatalf("write multi-doc: %v", err)
	}
	if err := run([]string{"validate", "--config", configPath}, strings.NewReader(""), &stdout, &stderr); err == nil {
		t.Fatal("expected multi-doc rejection")
	}
}

func TestRunHashKeyStdoutOnlyHashDespiteLogDiagnostics(t *testing.T) {
	dir := t.TempDir()
	configPath, _, saltPath, _ := writeCLIConfig(t, dir, "")
	hasher, err := hash.LoadValidated(saltPath)
	if err != nil {
		t.Fatalf("LoadValidated: %v", err)
	}
	want := hasher.Hash("sk-only-stdout")

	var stdout, stderr bytes.Buffer
	// Force any log diagnostics into stderr buffer; stdout must stay pure.
	prev := log.Writer()
	log.SetOutput(&stderr)
	defer log.SetOutput(prev)

	if err := run([]string{"hash-key", "--config", configPath}, strings.NewReader("sk-only-stdout\n"), &stdout, &stderr); err != nil {
		t.Fatalf("hash-key: %v stderr=%s", err, stderr.String())
	}
	if stdout.String() != want+"\n" {
		t.Fatalf("stdout = %q, want exact hash line", stdout.String())
	}
	if strings.Contains(stdout.String(), "sk-only-stdout") {
		t.Fatal("stdin echoed to stdout")
	}
	if strings.Contains(stderr.String(), "sk-only-stdout") {
		t.Fatal("stdin leaked to stderr")
	}
}
