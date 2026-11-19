package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/astraive/loza/sdks/go"
)

// envVars are the environment variables exercised by the env-configuration
// example. They are kept in one place so that every Setenv call is checked.
var envVars = [][2]string{
	{"LOZA_SERVICE_NAME", "env-service"},
	{"LOZA_SERVICE_VERSION", "2.0.0"},
	{"LOZA_ENVIRONMENT", "staging"},
	{"LOZA_COLLECTOR_URL", "http://env-collector:9308"},
	{"LOZA_TENANT_ID", "tenant-env"},
	{"LOZA_FLUSH_INTERVAL", "15s"},
}

// setDemoEnvVars applies the demo's environment variables with the supplied
// setter and returns a descriptive error on the first failure. Passing a nil
// setter uses os.Setenv. Callers must handle the returned error before relying
// on the environment-derived configuration.
func setDemoEnvVars(setenv func(string, string) error) error {
	if setenv == nil {
		setenv = os.Setenv
	}
	for _, kv := range envVars {
		if err := setenv(kv[0], kv[1]); err != nil {
			return fmt.Errorf("os.Setenv(%q, %q): %w", kv[0], kv[1], err)
		}
	}
	return nil
}

// runEnvExample prints the environment-configuration example. If any variable
// cannot be set, it reports the error and returns it without printing values
// that would wrongly appear to come from env configuration.
func runEnvExample(w io.Writer, setenv func(string, string) error) error {
	fmt.Fprintln(w, "2. Configuration via environment variables:")
	if err := setDemoEnvVars(setenv); err != nil {
		fmt.Fprintf(w, "  ✗ Unable to configure environment: %v\n", err)
		fmt.Fprintln(w)
		return err
	}

	cfg2 := loza.Dev()
	cfg2 = loza.LoadFromEnv(cfg2)
	fmt.Fprintf(w, "  Service: %s\n", cfg2.Service)
	fmt.Fprintf(w, "  Version: %s\n", cfg2.Version)
	fmt.Fprintf(w, "  Environment: %s\n", cfg2.Environment)
	fmt.Fprintf(w, "  CollectorURL: %s\n", cfg2.CollectorURL)
	fmt.Fprintf(w, "  TenantID: %s\n", cfg2.TenantID)
	fmt.Fprintf(w, "  FlushInterval: %s\n\n", cfg2.FlushInterval)
	return nil
}

func main() {
	fmt.Println("=== LOZA SDK Configuration Demo ===")
	fmt.Println()

	// Example 1: Configuration via code (highest precedence)
	fmt.Println("1. Configuration via code:")
	cfg1 := loza.Dev()
	cfg1 = loza.ApplyConfig(cfg1,
		loza.WithService("my-service"),
		loza.WithVersion("0.2.6"),
		loza.WithEnvironment("production"),
		loza.WithCollectorURL("http://localhost:9308"),
		loza.WithTenantID("tenant-123"),
		loza.WithBatchSize(200),
		loza.WithFlushInterval(10*time.Second),
		loza.WithMaxRetries(5),
		loza.WithTimeout(30*time.Second),
	)
	fmt.Printf("  Service: %s\n", cfg1.Service)
	fmt.Printf("  Version: %s\n", cfg1.Version)
	fmt.Printf("  Environment: %s\n", cfg1.Environment)
	fmt.Printf("  CollectorURL: %s\n", cfg1.CollectorURL)
	fmt.Printf("  TenantID: %s\n", cfg1.TenantID)
	fmt.Printf("  BatchSize: %d\n", cfg1.BatchSize)
	fmt.Printf("  FlushInterval: %s\n", cfg1.FlushInterval)
	fmt.Printf("  MaxRetries: %d\n", cfg1.MaxRetries)
	fmt.Printf("  Timeout: %s\n\n", cfg1.Timeout)

	// Example 2: Configuration via environment variables. Stop the demo if the
	// environment cannot be configured, since later examples rely on those values.
	if err := runEnvExample(os.Stdout, os.Setenv); err != nil {
		fmt.Printf("  Aborting demo: environment configuration failed.\n")
		return
	}

	// Example 3: Configuration precedence (code > env > defaults)
	fmt.Println("3. Configuration precedence (code > env > defaults):")
	cfg3 := loza.Dev()            // defaults
	cfg3 = loza.LoadFromEnv(cfg3) // apply env vars
	cfg3 = loza.ApplyConfig(cfg3, // apply code config (highest precedence)
		loza.WithService("code-service"),
		loza.WithCollectorURL("http://code-collector:9308"),
	)
	fmt.Printf("  Service: %s (from code, overrides env)\n", cfg3.Service)
	fmt.Printf("  Version: %s (from env)\n", cfg3.Version)
	fmt.Printf("  CollectorURL: %s (from code, overrides env)\n", cfg3.CollectorURL)
	fmt.Printf("  TenantID: %s (from env)\n", cfg3.TenantID)
	fmt.Printf("  FlushInterval: %s (from env)\n\n", cfg3.FlushInterval)

	// Example 4: Configuration validation
	fmt.Println("4. Configuration validation:")
	invalidCfg := loza.Config{
		BatchSize: -1, // invalid
	}
	if err := invalidCfg.Validate(); err != nil {
		fmt.Printf("  ✓ Validation caught error: %v\n", err)
	}

	validCfg := loza.Dev()
	if err := validCfg.Validate(); err != nil {
		fmt.Printf("  ✗ Unexpected validation error: %v\n", err)
	} else {
		fmt.Printf("  ✓ Valid configuration passed validation\n")
	}
	fmt.Println()

	// Example 5: Using the configured logger
	fmt.Println("5. Using the configured logger:")
	logger, err := loza.New(cfg1)
	if err != nil {
		fmt.Printf("  Error creating logger: %v\n", err)
		return
	}
	defer logger.Shutdown(context.Background())

	logger.Info("Configuration demo completed successfully")
	fmt.Println("  ✓ Logger created and event emitted")
	fmt.Println()

	// Example 6: Flush and Shutdown
	fmt.Println("6. Flush and Shutdown:")
	if err := logger.Flush(context.Background()); err != nil {
		fmt.Printf("  Error flushing: %v\n", err)
	} else {
		fmt.Println("  ✓ Flush completed successfully")
	}

	if err := logger.Shutdown(context.Background()); err != nil {
		fmt.Printf("  Error shutting down: %v\n", err)
	} else {
		fmt.Println("  ✓ Shutdown completed successfully")
	}

	fmt.Println("\n=== Demo Complete ===")
}
