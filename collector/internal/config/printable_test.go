package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// expectedSecretFields enumerates every field that carries a credential or a
// credential reference. It must stay in sync with printableAllowlist: these
// fields must never be allowlisted, and every other schema field must be.
var expectedSecretFields = []string{
	"AuthConfig.ValueEnv",
	"AuthConfig.Value",
	"AuthConfig.ServerSecret",
	"AuthGrantConfig.PasswordEnv",
	"AuthGrantConfig.PublicIDEnv",
	"AuthKeyConfig.SecretEnv",
	"AuthTokenConfig.TokenEnv",
	"StorageConfig.EncryptionKeyEnv",
	"StorageConfig.EncryptionKey",
	"DatabaseConnectionConfig.UsernameEnv",
	"DatabaseConnectionConfig.PasswordEnv",
	"FanoutClickHouseConfig.Password",
	"FanoutPostgresConfig.DSN",
	"FanoutS3Config.AccessKey",
	"FanoutS3Config.SecretKey",
	"FanoutGCSConfig.CredentialsFile",
	"DedupeConfig.Key",
	"DedupeConfig.RedisPassword",
	"CortexBridgeConfig.APIKey",
	"EBRedisConfig.Password",
	"EBNATSConfig.Password",
}

func allSchemaFieldKeys() map[string]struct{} {
	found := map[string]struct{}{}
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		if t == timeType || t == durationType {
			return
		}
		seen[t] = true
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			found[t.Name()+"."+f.Name] = struct{}{}
			walk(f.Type)
		}
	}
	walk(reflect.TypeOf(Config{}))
	return found
}

func TestPrintableAllowlistIsExact(t *testing.T) {
	all := allSchemaFieldKeys()

	expected := map[string]struct{}{}
	for _, key := range expectedSecretFields {
		expected[key] = struct{}{}
	}

	// Every allowlisted key must correspond to a real schema field (catches
	// typos) and must not be a known secret field.
	for key := range printableAllowlist {
		if _, ok := all[key]; !ok {
			t.Errorf("allowlist contains unknown field %q", key)
		}
		if _, ok := expected[key]; ok {
			t.Errorf("allowlist must not contain secret field %q", key)
		}
	}

	// Every schema field that is not allowlisted is redacted by construction;
	// the complete set of such fields must be exactly the known secrets. This
	// makes a newly added field fail the test until it is explicitly reviewed.
	var unallowlisted []string
	for key := range all {
		if _, ok := printableAllowlist[key]; !ok {
			unallowlisted = append(unallowlisted, key)
		}
	}
	sort.Strings(unallowlisted)
	want := append([]string(nil), expectedSecretFields...)
	sort.Strings(want)
	if !reflect.DeepEqual(unallowlisted, want) {
		t.Errorf("redacted (non-allowlisted) fields changed.\n got: %v\nwant: %v", unallowlisted, want)
	}
}

// TestPrintableYAMLOmitsSecrets exercises the same encode path used by
// `config print` (marshalPrintableConfig) without pulling in the full command
// dependency tree, so it still runs in dependency-restricted environments.
func TestPrintableYAMLOmitsSecrets(t *testing.T) {
	cfg := Default()
	secrets := []string{
		"yaml-secret-server", "yaml-secret-value", "yaml-secret-encryption",
		"yaml-secret-dsn", "yaml-secret-password", "yaml-secret-key",
	}
	cfg.Auth.ServerSecret = secrets[0]
	cfg.Auth.Value = secrets[1]
	cfg.Storage.EncryptionKey = secrets[2]
	cfg.Fanout.Outputs = []FanoutOutputConfig{{
		ClickHouse: FanoutClickHouseConfig{Password: secrets[3]},
		Postgres:   FanoutPostgresConfig{DSN: secrets[4]},
		S3:         FanoutS3Config{SecretKey: secrets[5]},
	}}

	raw, err := yaml.Marshal(cfg.Printable())
	if err != nil {
		t.Fatalf("marshal printable config: %v", err)
	}
	printed := string(raw)
	for _, secret := range secrets {
		if strings.Contains(printed, secret) {
			t.Errorf("secret %q leaked into printable YAML:\n%s", secret, printed)
		}
	}
	if !strings.Contains(printed, RedactedMarker) {
		t.Errorf("expected %q marker in printable YAML", RedactedMarker)
	}
	if !strings.Contains(printed, cfg.Collector.Addr) {
		t.Errorf("non-secret field collector.addr missing from printable YAML")
	}
}

func TestPrintableRedactsAndDoesNotMutate(t *testing.T) {
	cfg := Default()
	cfg.Auth.ServerSecret = "top-secret-server-value"
	cfg.Auth.Value = "top-secret-api-value"
	cfg.Storage.EncryptionKey = "top-secret-encryption-value"
	cfg.Fanout.Outputs = []FanoutOutputConfig{{
		Postgres: FanoutPostgresConfig{DSN: "postgres://user:top-secret-dsn@host/db"},
	}}

	printable := cfg.Printable()
	if printable.Auth.ServerSecret != RedactedMarker {
		t.Errorf("server secret not redacted: %q", printable.Auth.ServerSecret)
	}
	if printable.Auth.Value != RedactedMarker {
		t.Errorf("api value not redacted: %q", printable.Auth.Value)
	}
	if printable.Storage.EncryptionKey != RedactedMarker {
		t.Errorf("encryption key not redacted: %q", printable.Storage.EncryptionKey)
	}
	if got := printable.Fanout.Outputs[0].Postgres.DSN; got != RedactedMarker {
		t.Errorf("dsn not redacted: %q", got)
	}
	if printable.Collector.Addr != cfg.Collector.Addr {
		t.Errorf("non-secret field was redacted: %q", printable.Collector.Addr)
	}

	// The original config must be untouched.
	if cfg.Auth.ServerSecret != "top-secret-server-value" ||
		cfg.Auth.Value != "top-secret-api-value" ||
		cfg.Fanout.Outputs[0].Postgres.DSN != "postgres://user:top-secret-dsn@host/db" {
		t.Fatal("Printable mutated the source config")
	}

	// No secret value may survive anywhere in the printable tree.
	var visit func(reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if !v.IsNil() {
				visit(v.Elem())
			}
		case reflect.Struct:
			if v.Type() == timeType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				visit(iter.Value())
			}
		case reflect.String:
			if strings.Contains(v.String(), "top-secret") {
				t.Errorf("secret survived redaction: %q", v.String())
			}
		}
	}
	visit(reflect.ValueOf(printable))
}
