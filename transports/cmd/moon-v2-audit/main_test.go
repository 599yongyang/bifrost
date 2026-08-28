package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditPluginPathBlocksUnallowlistedPrivateArtifacts(t *testing.T) {
	findings := auditPluginPath(0, "moon", "https://127.0.0.1/plugin.so", nil)
	if len(findings) != 1 || findings[0].Severity != severityError || findings[0].Code != "private-plugin-download-blocked" {
		t.Fatalf("unexpected findings: %#v", findings)
	}
	allowlist, parseFindings := parsePluginDownloadAllowlist([]string{"127.0.0.1"})
	if len(parseFindings) != 0 {
		t.Fatalf("unexpected allowlist findings: %#v", parseFindings)
	}
	if allowed := auditPluginPath(0, "moon", "https://127.0.0.1/plugin.so", allowlist); len(allowed) != 1 || allowed[0].Severity != severityInfo {
		t.Fatalf("expected allowlisted remote artifact info, got %#v", allowed)
	}
}

func TestPublicIPMatchesHardenedLiteralPolicy(t *testing.T) {
	for _, host := range []string{"100.64.0.1", "0.0.0.0", "::", "ff02::1", "2002:7f00:1::", "64:ff9b::7f00:1"} {
		if isPublicIP(net.ParseIP(host)) {
			t.Fatalf("expected %s to be blocked", host)
		}
	}
}

func TestAuditPluginPathWarnsWhenHostnameDNSCannotBeProvenOffline(t *testing.T) {
	findings := auditPluginPath(0, "moon", "https://artifacts.example.com/plugin.so", nil)
	if len(findings) != 2 || findings[1].Code != "plugin-host-dns-unverified" || findings[1].Severity != severityWarn {
		t.Fatalf("expected DNS warning, got %#v", findings)
	}
}

func TestParsePluginDownloadAllowlistRejectsServerInvalidSyntax(t *testing.T) {
	_, findings := parsePluginDownloadAllowlist([]string{"https://artifact.internal", "*.internal", "10.0.0.999"})
	if len(findings) != 3 {
		t.Fatalf("expected every malformed entry to fail, got %#v", findings)
	}
}

func TestAuditDocumentWarnsAboutContainerPathsWithoutPrintingValues(t *testing.T) {
	config := configDocument{EncryptionKey: "configured"}
	config.ConfigStore.Enabled = true
	config.ConfigStore.Type = "sqlite"
	config.ConfigStore.Config.Path = "/Users/operator/private/config.db"
	config.Plugins = append(config.Plugins, struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}{Name: "moon", Path: "/Users/operator/private/moon.so"})

	findings := auditDocument(config, "config.json")
	if len(findings) < 2 {
		t.Fatalf("expected path findings, got %#v", findings)
	}
	for _, item := range findings {
		if strings.Contains(item.Message, "/Users/operator/private") {
			t.Fatalf("finding leaked config value: %#v", item)
		}
	}
}

func TestScanDeprecatedUsageReportsLocationWithoutSourceLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "caller.env")
	if err := os.WriteFile(path, []byte("HEADER=x-bf-prom-secret-label\nROUTE=/api/governance/routing-rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	findings, err := scanDeprecatedUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected two findings, got %#v", findings)
	}
	for _, item := range findings {
		if !strings.HasPrefix(item.Location, path+":") {
			t.Fatalf("expected file and line location, got %#v", item)
		}
		if strings.Contains(item.Message, "secret-label") {
			t.Fatalf("finding leaked source content: %#v", item)
		}
	}
}

func TestScanDeprecatedUsageDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.env")
	if err := os.WriteFile(target, []byte("HEADER=x-bf-prom-private"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.env")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	findings, err := scanDeprecatedUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Code != "scan-symlink-skipped" {
		t.Fatalf("expected a symlink warning without target scan, got %#v", findings)
	}
}

func TestResolveSchemaPathUsesExplicitPath(t *testing.T) {
	if got, err := resolveSchemaPath("/secure/config.schema.json"); err != nil || got != "/secure/config.schema.json" {
		t.Fatalf("unexpected explicit schema result: %q, %v", got, err)
	}
}

func TestRunAuditDoesNotExposeInvalidConfigValues(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(configPath, []byte(`{"encryption_key":"super-secret-value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","properties":{"encryption_key":{"type":"integer"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	findings, err := runAudit(configPath, schemaPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 || findings[0].Code != "schema-invalid" {
		t.Fatalf("expected schema finding, got %#v", findings)
	}
	for _, item := range findings {
		if strings.Contains(item.Message, "super-secret-value") {
			t.Fatalf("finding leaked invalid value: %#v", item)
		}
	}
}
