package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type severity string

const (
	severityError severity = "ERROR"
	severityWarn  severity = "WARN"
	severityInfo  severity = "INFO"
)

type finding struct {
	Severity severity
	Code     string
	Location string
	Message  string
}

type configDocument struct {
	Version       *int   `json:"version"`
	EncryptionKey string `json:"encryption_key"`
	SetupToken    string `json:"setup_token"`
	Server        struct {
		PluginDownloadPrivateAllowlist []string `json:"plugin_download_private_allowlist"`
	} `json:"server"`
	AuthConfig struct {
		Enabled bool `json:"enabled"`
	} `json:"auth_config"`
	ConfigStore storeConfig `json:"config_store"`
	LogsStore   storeConfig `json:"logs_store"`
	Plugins     []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"plugins"`
}

type storeConfig struct {
	Enabled bool `json:"enabled"`
	Type    string
	Config  struct {
		Path string `json:"path"`
	} `json:"config"`
}

type scanPattern struct {
	Code    string
	Needle  string
	Message string
}

var deprecatedPatterns = []scanPattern{
	{Code: "deprecated-prom-header", Needle: "x-bf-prom-", Message: "replace x-bf-prom-* with supported custom labels or x-bf-dim-*"},
	{Code: "legacy-routing-rules-api", Needle: "/api/governance/routing-rules", Message: "move callers to /api/routing/rules"},
	{Code: "legacy-complexity-api", Needle: "/api/governance/complexity-analyzer-config", Message: "move callers to /api/routing/complexity-analyzer-config"},
}

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

func main() {
	configPath := flag.String("config", "", "path to the production config.json copy")
	schemaPath := flag.String("schema", "", "path to the v2 config schema (auto-detected when omitted)")
	var scanRoots stringListFlag
	flag.Var(&scanRoots, "scan", "file or directory containing deployment manifests/caller configs (repeatable)")
	flag.Parse()

	if strings.TrimSpace(*configPath) == "" {
		fmt.Fprintln(os.Stderr, "-config is required")
		os.Exit(2)
	}

	resolvedSchema, err := resolveSchemaPath(*schemaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit failed: %v\n", err)
		os.Exit(2)
	}
	findings, err := runAudit(*configPath, resolvedSchema, scanRoots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit failed: %v\n", err)
		os.Exit(2)
	}
	for _, item := range findings {
		fmt.Printf("%s %-30s %s: %s\n", item.Severity, item.Code, item.Location, item.Message)
	}
	if len(findings) == 0 {
		fmt.Println("PASS no compatibility findings")
	}
	for _, item := range findings {
		if item.Severity == severityError {
			os.Exit(1)
		}
	}
}

func resolveSchemaPath(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	for _, candidate := range []string{"transports/config.schema.json", "config.schema.json"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("v2 config schema not found; pass -schema")
}

func runAudit(configPath, schemaPath string, scanRoots []string) ([]finding, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return []finding{{Severity: severityError, Code: "empty-config", Location: configPath, Message: "config file is empty"}}, nil
	}
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}

	findings := make([]finding, 0)
	if err := validateSchema(data, schema); err != nil {
		findings = append(findings, finding{Severity: severityError, Code: "schema-invalid", Location: configPath, Message: "config does not match the v2 schema; inspect validator details only in a secure environment"})
	}

	var config configDocument
	if err := json.Unmarshal(data, &config); err != nil {
		findings = append(findings, finding{Severity: severityError, Code: "invalid-json", Location: configPath, Message: "config is not valid JSON"})
		return findings, nil
	}
	findings = append(findings, auditDocument(config, configPath)...)
	for _, root := range scanRoots {
		scanned, err := scanDeprecatedUsage(root)
		if err != nil {
			return nil, err
		}
		findings = append(findings, scanned...)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity < findings[j].Severity
		}
		if findings[i].Location != findings[j].Location {
			return findings[i].Location < findings[j].Location
		}
		return findings[i].Code < findings[j].Code
	})
	return findings, nil
}

func validateSchema(data, schema []byte) error {
	schemaDocument, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("config.schema.json", schemaDocument); err != nil {
		return err
	}
	compiled, err := compiler.Compile("config.schema.json")
	if err != nil {
		return err
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return err
	}
	return compiled.Validate(document)
}

func auditDocument(config configDocument, configPath string) []finding {
	findings := make([]finding, 0)
	if config.Version == nil {
		findings = append(findings, finding{Severity: severityInfo, Code: "version-default", Location: "version", Message: "version is omitted; v2 empty-list semantics apply"})
	} else if *config.Version == 1 {
		findings = append(findings, finding{Severity: severityWarn, Code: "legacy-list-semantics", Location: "version", Message: "version=1 preserves legacy empty-list allow-all behavior; verify this is intentional"})
	}
	if config.ConfigStore.Enabled && strings.TrimSpace(config.EncryptionKey) == "" {
		findings = append(findings, finding{Severity: severityWarn, Code: "missing-encryption-key", Location: "encryption_key", Message: "config store is enabled but secrets may be persisted without encryption"})
	}
	if config.AuthConfig.Enabled && strings.TrimSpace(config.SetupToken) == "" {
		findings = append(findings, finding{Severity: severityInfo, Code: "setup-token-external", Location: "setup_token", Message: "confirm BIFROST_SETUP_TOKEN is supplied if first-admin bootstrap is still required"})
	}
	findings = append(findings, auditStorePath("config_store", config.ConfigStore)...)
	findings = append(findings, auditStorePath("logs_store", config.LogsStore)...)
	allowlist, allowlistFindings := parsePluginDownloadAllowlist(config.Server.PluginDownloadPrivateAllowlist)
	findings = append(findings, allowlistFindings...)
	for index, plugin := range config.Plugins {
		findings = append(findings, auditPluginPath(index, plugin.Name, plugin.Path, allowlist)...)
	}
	if len(findings) == 0 {
		findings = append(findings, finding{Severity: severityInfo, Code: "config-audited", Location: configPath, Message: "schema and compatibility checks passed"})
	}
	return findings
}

func auditStorePath(location string, store storeConfig) []finding {
	if !store.Enabled || store.Type != "sqlite" || strings.TrimSpace(store.Config.Path) == "" {
		return nil
	}
	if !strings.HasPrefix(filepath.Clean(store.Config.Path), "/app/data/") {
		return []finding{{Severity: severityWarn, Code: "container-store-path", Location: location + ".config.path", Message: "SQLite path is outside /app/data; verify the path is mounted and writable by uid 1000"}}
	}
	return nil
}

func auditPluginPath(index int, name, path string, allowlist *pluginDownloadAllowlist) []finding {
	location := fmt.Sprintf("plugins[%d].path", index)
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		host := parsed.Hostname()
		if ip := net.ParseIP(host); ip != nil && !isPublicIP(ip) && !allowlist.permits(host, ip) {
			return []finding{{Severity: severityError, Code: "private-plugin-download-blocked", Location: location, Message: "private plugin artifact host is not present in server.plugin_download_private_allowlist"}}
		}
		if net.ParseIP(host) == nil && !allowlist.permits(host, nil) {
			return []finding{
				{Severity: severityInfo, Code: "remote-plugin-artifact", Location: location, Message: "remote plugin artifact configured; record its digest and verify download policy"},
				{Severity: severityWarn, Code: "plugin-host-dns-unverified", Location: location, Message: "offline audit cannot prove hostname resolution is public; verify download from the release runtime or allowlist the exact trusted host"},
			}
		}
		return []finding{{Severity: severityInfo, Code: "remote-plugin-artifact", Location: location, Message: "remote plugin artifact configured; record its digest and verify download policy"}}
	}
	if !filepath.IsAbs(trimmed) {
		return []finding{{Severity: severityWarn, Code: "relative-plugin-path", Location: location, Message: "use an absolute container path for dynamic plugin " + safeName(name)}}
	}
	if !strings.HasPrefix(filepath.Clean(trimmed), "/app/data/plugins/") {
		return []finding{{Severity: severityWarn, Code: "container-plugin-path", Location: location, Message: "plugin path is outside /app/data/plugins; verify the file exists inside the runtime container"}}
	}
	return nil
}

type pluginDownloadAllowlist struct {
	hostnames map[string]struct{}
	prefixes  []netip.Prefix
}

func parsePluginDownloadAllowlist(entries []string) (*pluginDownloadAllowlist, []finding) {
	allowlist := &pluginDownloadAllowlist{hostnames: make(map[string]struct{})}
	findings := make([]finding, 0)
	for index, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			allowlist.prefixes = append(allowlist.prefixes, prefix)
			continue
		}
		if address, err := netip.ParseAddr(entry); err == nil {
			bits := 32
			if address.Is6() {
				bits = 128
			}
			allowlist.prefixes = append(allowlist.prefixes, netip.PrefixFrom(address, bits))
			continue
		}
		if looksLikeIPLiteral(entry) || !isValidHostnameLiteral(entry) {
			findings = append(findings, finding{Severity: severityError, Code: "invalid-plugin-download-allowlist", Location: fmt.Sprintf("server.plugin_download_private_allowlist[%d]", index), Message: "entry must be a bare hostname, IP address, or CIDR without scheme, path, port, or wildcard"})
			continue
		}
		allowlist.hostnames[strings.ToLower(entry)] = struct{}{}
	}
	return allowlist, findings
}

func looksLikeIPLiteral(value string) bool {
	if strings.Contains(value, ":") {
		return true
	}
	labels := strings.Split(value, ".")
	if len(labels) != 4 {
		return false
	}
	for _, label := range labels {
		if label == "" {
			return false
		}
		for _, character := range label {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func (allowlist *pluginDownloadAllowlist) permits(host string, ip net.IP) bool {
	if allowlist == nil {
		return false
	}
	if _, ok := allowlist.hostnames[strings.ToLower(host)]; ok {
		return true
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if embedded, ok := embeddedIPv4(address); ok {
		address = embedded
	}
	for _, prefix := range allowlist.prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func isPublicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if embedded, ok := embeddedIPv4(address); ok {
		address = embedded
	}
	if address.IsLoopback() || address.IsPrivate() || cgnatPrefix.Contains(address) || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() || address.IsInterfaceLocalMulticast() ||
		address == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return false
	}
	if address.Is6() {
		bytes := address.As16()
		if bytes[0] == 0xfe && bytes[1]&0xc0 == 0xc0 {
			return false
		}
	}
	return true
}

func embeddedIPv4(address netip.Addr) (netip.Addr, bool) {
	if !address.Is6() {
		return netip.Addr{}, false
	}
	bytes := address.As16()
	switch {
	case bytes[0] == 0x20 && bytes[1] == 0x02:
		return netip.AddrFrom4([4]byte{bytes[2], bytes[3], bytes[4], bytes[5]}), true
	case bytes[0] == 0x00 && bytes[1] == 0x64 && bytes[2] == 0xff && bytes[3] == 0x9b:
		if bytes[4] == 0x00 && bytes[5] == 0x00 {
			return netip.AddrFrom4([4]byte{bytes[12], bytes[13], bytes[14], bytes[15]}), true
		}
		if bytes[4] == 0x00 && bytes[5] == 0x01 {
			return netip.AddrFrom4([4]byte{bytes[6], bytes[7], bytes[9], bytes[10]}), true
		}
	}
	return netip.Addr{}, false
}

func isValidHostnameLiteral(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for index, character := range label {
			switch {
			case character == '-':
				if index == 0 || index == len(label)-1 {
					return false
				}
			case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
			default:
				return false
			}
		}
	}
	return true
}

func scanDeprecatedUsage(root string) ([]finding, error) {
	findings := make([]finding, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			findings = append(findings, finding{Severity: severityWarn, Code: "scan-symlink-skipped", Location: path, Message: "symbolic link was not followed; scan its trusted target explicitly if required"})
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			findings = append(findings, finding{Severity: severityWarn, Code: "scan-file-unreadable", Location: path, Message: "file metadata could not be read"})
			return nil
		}
		if info.Size() > 4<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			findings = append(findings, finding{Severity: severityWarn, Code: "scan-file-unreadable", Location: path, Message: "file could not be read"})
			return nil
		}
		if strings.IndexByte(string(data), 0) >= 0 {
			return nil
		}
		for _, pattern := range deprecatedPatterns {
			for lineIndex, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, pattern.Needle) {
					findings = append(findings, finding{Severity: severityWarn, Code: pattern.Code, Location: fmt.Sprintf("%s:%d", path, lineIndex+1), Message: pattern.Message})
				}
			}
		}
		return nil
	})
	return findings, err
}

func safeName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "plugin"
	}
	return fmt.Sprintf("%q", name)
}

type stringListFlag []string

func (values *stringListFlag) String() string { return strings.Join(*values, ",") }
func (values *stringListFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}
