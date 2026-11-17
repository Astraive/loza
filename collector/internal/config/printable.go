package config

import (
	"reflect"
	"time"
)

// RedactedMarker replaces the value of every field that is not explicitly
// allowlisted for printing.
const RedactedMarker = "<redacted>"

// printableAllowlist is the complete set of struct fields whose real values may
// appear in the output of a printable config. Keys are "<StructType>.<Field>".
//
// This is deliberately an allowlist rather than a denylist: a field is printed
// verbatim only after a reviewer has explicitly added it here. Any field added
// to the schema in the future is redacted by default until it is reviewed, so a
// newly introduced secret can never silently leak through `config print`.
var printableAllowlist = map[string]struct{}{
	// Config
	"Config.Version": {}, "Config.Collector": {}, "Config.Auth": {},
	"Config.RateLimit": {}, "Config.Routes": {}, "Config.Storage": {},
	"Config.Database": {}, "Config.DuckDB": {}, "Config.LQL": {},
	"Config.Retention": {}, "Config.Kafka": {}, "Config.Worker": {},
	"Config.Logging": {}, "Config.Metrics": {}, "Config.Reliability": {},
	"Config.Limits": {}, "Config.Identity": {}, "Config.Privacy": {},
	"Config.Components": {}, "Config.Retry": {}, "Config.DeadLetter": {},
	"Config.Fanout": {}, "Config.Dedupe": {}, "Config.Schema": {},
	"Config.CortexBridge": {}, "Config.EventBus": {},

	// CollectorConfig / ServerConfig / protocol servers
	"CollectorConfig.Addr": {}, "CollectorConfig.ReadHeaderTimeout": {},
	"CollectorConfig.ShutdownTimeout": {}, "CollectorConfig.MaxBodyBytes": {},
	"CollectorConfig.MaxEventsPerReq": {}, "CollectorConfig.Server": {},
	"ServerConfig.HTTP": {}, "ServerConfig.GRPC": {}, "ServerConfig.GraphQL": {},
	"HTTPConfig.Enabled": {}, "HTTPConfig.Addr": {},
	"HTTPConfig.ReadHeaderTimeout": {}, "HTTPConfig.MaxBodyBytes": {},
	"HTTPConfig.MaxHeaderBytes": {}, "HTTPConfig.IdleTimeout": {}, "HTTPConfig.TLS": {},
	"GRPCConfig.Enabled": {}, "GRPCConfig.Port": {}, "GRPCConfig.MaxConnections": {},
	"GRPCConfig.MaxConcurrentStreams": {}, "GRPCConfig.MaxRecvMsgSize": {},
	"GRPCConfig.MaxSendMsgSize": {}, "GRPCConfig.Keepalive": {}, "GRPCConfig.TLS": {},
	"KeepaliveConfig.MaxConnectionAge": {}, "KeepaliveConfig.MaxConnectionAgeGrace": {},
	"KeepaliveConfig.Time": {}, "KeepaliveConfig.Timeout": {},
	"TLSConfig.Enabled": {}, "TLSConfig.CertFile": {}, "TLSConfig.KeyFile": {},
	"TLSConfig.ClientCAFile": {}, "TLSConfig.RequireClientCert": {},
	"GraphQLConfig.Enabled": {}, "GraphQLConfig.Port": {},
	"GraphQLConfig.Playground": {}, "GraphQLConfig.DepthLimit": {},
	"GraphQLConfig.BatchLimit": {},

	// AuthConfig. Secret-bearing fields (ValueEnv, Value, ServerSecret and the
	// credential *_env references in the nested types below) are intentionally
	// absent.
	"AuthConfig.Enabled": {}, "AuthConfig.Header": {},
	"AuthConfig.AllowLocalDevKeys": {}, "AuthConfig.CacheTTL": {},
	"AuthConfig.NegativeCacheTTL": {}, "AuthConfig.DefaultCollector": {},
	"AuthConfig.Collectors": {}, "AuthConfig.Grants": {},
	"AuthConfig.Keys": {}, "AuthConfig.Tokens": {},
	"AuthCollectorConfig.Slug": {},
	"AuthGrantConfig.Name":     {}, "AuthGrantConfig.Collector": {},
	"AuthGrantConfig.Username": {}, "AuthGrantConfig.Permissions": {},
	"AuthGrantConfig.AllowedEnvs": {}, "AuthGrantConfig.AllowedServices": {},
	"AuthGrantConfig.AllowedOrigins": {}, "AuthGrantConfig.AllowedIPs": {},
	"AuthGrantConfig.MaxPayloadBytes": {}, "AuthGrantConfig.MaxRequestsPerMinute": {},
	"AuthGrantConfig.MaxEventsPerMinute": {},
	"AuthKeyConfig.Name":                 {}, "AuthKeyConfig.KeyID": {}, "AuthKeyConfig.Kind": {},
	"AuthKeyConfig.Mode": {}, "AuthKeyConfig.Roles": {}, "AuthKeyConfig.Collector": {},
	"AuthKeyConfig.Permissions": {}, "AuthKeyConfig.AllowedEnvs": {},
	"AuthKeyConfig.AllowedServices": {}, "AuthKeyConfig.AllowedOrigins": {},
	"AuthKeyConfig.AllowedIPs": {}, "AuthKeyConfig.MaxPayloadBytes": {},
	"AuthKeyConfig.MaxRequestsPerMinute": {}, "AuthKeyConfig.MaxEventsPerMinute": {},
	"AuthTokenConfig.Name": {}, "AuthTokenConfig.Mode": {}, "AuthTokenConfig.Roles": {},
	"AuthTokenConfig.Collector": {}, "AuthTokenConfig.Permissions": {},
	"AuthTokenConfig.AllowedEnvs": {}, "AuthTokenConfig.AllowedServices": {},
	"AuthTokenConfig.AllowedOrigins": {}, "AuthTokenConfig.AllowedIPs": {},
	"AuthTokenConfig.MaxPayloadBytes": {}, "AuthTokenConfig.MaxRequestsPerMinute": {},
	"AuthTokenConfig.MaxEventsPerMinute": {},

	// Rate limiting, routes and storage (EncryptionKey / EncryptionKeyEnv are secrets).
	"RateLimitConfig.Enabled": {}, "RateLimitConfig.RPS": {}, "RateLimitConfig.Burst": {},
	"RoutesConfig.Ingest": {}, "RoutesConfig.Health": {}, "RoutesConfig.Ready": {},
	"RoutesConfig.Metrics":  {},
	"StorageConfig.Primary": {}, "StorageConfig.Connection": {},

	// Database connections (UsernameEnv / PasswordEnv are secrets).
	"DatabaseConfig.Connections":    {},
	"DatabaseConnectionConfig.Name": {}, "DatabaseConnectionConfig.Type": {},
	"DatabaseConnectionConfig.Enabled": {}, "DatabaseConnectionConfig.Path": {},
	"DatabaseConnectionConfig.Driver": {}, "DatabaseConnectionConfig.Host": {},
	"DatabaseConnectionConfig.Port": {}, "DatabaseConnectionConfig.Hosts": {},
	"DatabaseConnectionConfig.Database": {}, "DatabaseConnectionConfig.SSLMode": {},
	"DatabaseConnectionConfig.TLS": {}, "DatabaseConnectionConfig.Table": {},
	"DatabaseConnectionConfig.RawColumn": {}, "DatabaseConnectionConfig.StoreRaw": {},
	"DatabaseConnectionConfig.Schema": {}, "DatabaseConnectionConfig.ColumnTypes": {},
	"DatabaseConnectionConfig.ConnectionTimeout": {}, "DatabaseConnectionConfig.QueryTimeout": {},

	// DuckDB / LQL / retention / Kafka / worker / logging / metrics
	"DuckDBConfig.Path": {}, "DuckDBConfig.Driver": {}, "DuckDBConfig.Table": {},
	"DuckDBConfig.RawColumn": {}, "DuckDBConfig.StoreRaw": {},
	"DuckDBConfig.CheckpointOnShutdown": {}, "DuckDBConfig.CheckpointInterval": {},
	"DuckDBConfig.MaxOpenConns": {}, "DuckDBConfig.MaxIdleConns": {},
	"DuckDBConfig.BatchSize": {}, "DuckDBConfig.FlushInterval": {},
	"DuckDBConfig.WriterLoop": {}, "DuckDBConfig.WriterQueueSize": {},
	"DuckDBConfig.UseAppender": {}, "DuckDBConfig.WriteTimeout": {},
	"DuckDBConfig.RetryAttempts": {}, "DuckDBConfig.RetryBackoff": {},
	"DuckDBConfig.Export": {}, "DuckDBConfig.Schema": {}, "DuckDBConfig.ColumnTypes": {},
	"LQLConfig.Enabled": {}, "LQLConfig.Binary": {},
	"LQLConfig.ExpectedProtocolVersion": {}, "LQLConfig.ExpectedCompilerVersion": {},
	"LQLConfig.ExpectedLanguageVersion": {}, "LQLConfig.StartupTimeout": {},
	"LQLConfig.CompileTimeout": {}, "LQLConfig.MaxConcurrentRequests": {},
	"DuckDBExport.Enabled": {}, "DuckDBExport.Format": {},
	"DuckDBExport.Interval": {}, "DuckDBExport.Path": {},
	"RetentionConfig.Enabled": {}, "RetentionConfig.Days": {}, "RetentionConfig.MaxSize": {},
	"KafkaConfig.Brokers": {}, "KafkaConfig.Topic": {}, "KafkaConfig.Acks": {},
	"KafkaConfig.RequestTimeout": {}, "KafkaConfig.EnableIdempotence": {},
	"KafkaConfig.MaxRetries": {}, "KafkaConfig.RetryBackoff": {},
	"WorkerConfig.ConsumerGroup": {}, "WorkerConfig.PollTimeout": {},
	"LoggingConfig.Level": {}, "LoggingConfig.Format": {},
	"MetricsConfig.Prometheus": {},

	// Reliability, limits, identity, privacy, components, retry, dead letter
	"ReliabilityConfig.Mode": {}, "ReliabilityConfig.SpoolDir": {},
	"ReliabilityConfig.SpoolFile": {}, "ReliabilityConfig.MaxSpoolBytes": {},
	"ReliabilityConfig.Fsync": {}, "ReliabilityConfig.DeliveryQueueSize": {},
	"ReliabilityConfig.QueueDir": {}, "ReliabilityConfig.QueueBatchSize": {},
	"ReliabilityConfig.QueueBatchTimeout": {}, "ReliabilityConfig.QueueFlushInterval": {},
	"ReliabilityConfig.QueueCircuitThreshold": {}, "ReliabilityConfig.QueueCircuitTimeout": {},
	"LimitsConfig.MaxInflightRequests": {}, "LimitsConfig.MaxInflightEvents": {},
	"LimitsConfig.MaxQueueBytes": {}, "LimitsConfig.MaxEventBytes": {},
	"LimitsConfig.MaxAttrCount": {}, "LimitsConfig.MaxAttrDepth": {},
	"LimitsConfig.MaxStringLength": {},
	"IdentityConfig.Mode":          {}, "IdentityConfig.AuthIdentityWins": {},
	"IdentityConfig.AllowPayloadIdentity": {}, "IdentityConfig.ServiceName": {},
	"IdentityConfig.ServiceVersion": {}, "IdentityConfig.DeploymentEnvironment": {},
	"IdentityConfig.DeploymentRegion": {}, "IdentityConfig.TenantID": {},
	"IdentityConfig.WorkspaceID": {}, "IdentityConfig.OrganizationID": {},
	"IdentityConfig.MTLSAllowedCNs": {}, "IdentityConfig.MTLSAllowedDNS": {},
	"IdentityConfig.MTLSAllowedEmails": {}, "IdentityConfig.AllowedOrigins": {},
	"IdentityConfig.TrustedProxies": {},
	"PrivacyConfig.Mode":            {}, "PrivacyConfig.CollectorRedaction": {},
	"PrivacyConfig.EmergencyRedaction": {}, "PrivacyConfig.Allowlist": {},
	"PrivacyConfig.Blocklist": {}, "PrivacyConfig.SecretScan": {},
	"PrivacyConfig.RightToDeleteEnabled": {},
	"ComponentRegistryConfig.Receivers":  {}, "ComponentRegistryConfig.Processors": {},
	"ComponentRegistryConfig.Exporters": {}, "ComponentRegistryConfig.Extensions": {},
	"RetryConfig.Enabled": {}, "RetryConfig.MaxAttempts": {},
	"RetryConfig.InitialBackoff": {}, "RetryConfig.MaxBackoff": {}, "RetryConfig.Jitter": {},
	"DeadLetterConfig.Enabled": {}, "DeadLetterConfig.Path": {},

	// Fanout (ClickHouse.Password, Postgres.DSN, S3.AccessKey / SecretKey and
	// GCS.CredentialsFile are secrets).
	"FanoutConfig.Outputs": {}, "FanoutConfig.Delivery": {},
	"FanoutOutputConfig.Name": {}, "FanoutOutputConfig.Role": {},
	"FanoutOutputConfig.Type": {}, "FanoutOutputConfig.Connection": {},
	"FanoutOutputConfig.Enabled": {}, "FanoutOutputConfig.DuckDB": {},
	"FanoutOutputConfig.Loki": {}, "FanoutOutputConfig.OTLP": {},
	"FanoutOutputConfig.ClickHouse": {}, "FanoutOutputConfig.Postgres": {},
	"FanoutOutputConfig.S3": {}, "FanoutOutputConfig.GCS": {},
	"FanoutDuckDBConfig.Path": {}, "FanoutDuckDBConfig.Table": {},
	"FanoutDuckDBConfig.RawColumn": {}, "FanoutDuckDBConfig.StoreRaw": {},
	"FanoutLokiConfig.URL": {}, "FanoutLokiConfig.TenantID": {},
	"FanoutLokiConfig.Labels": {}, "FanoutLokiConfig.BatchSize": {},
	"FanoutLokiConfig.FlushInterval": {}, "FanoutLokiConfig.Timeout": {},
	"FanoutOTLPConfig.Endpoint": {}, "FanoutOTLPConfig.Insecure": {},
	"FanoutOTLPConfig.Protocol": {}, "FanoutOTLPConfig.Timeout": {},
	"FanoutOTLPConfig.Logger":      {},
	"FanoutClickHouseConfig.Addrs": {}, "FanoutClickHouseConfig.Database": {},
	"FanoutClickHouseConfig.Username": {}, "FanoutClickHouseConfig.Table": {},
	"FanoutClickHouseConfig.Schema": {}, "FanoutClickHouseConfig.StoreRaw": {},
	"FanoutClickHouseConfig.RawColumn": {},
	"FanoutPostgresConfig.Table":       {}, "FanoutPostgresConfig.Schema": {},
	"FanoutPostgresConfig.StoreRaw": {}, "FanoutPostgresConfig.RawColumn": {},
	"FanoutS3Config.Bucket": {}, "FanoutS3Config.Prefix": {},
	"FanoutS3Config.Region": {}, "FanoutS3Config.Endpoint": {},
	"FanoutS3Config.Schema": {}, "FanoutS3Config.BatchSize": {},
	"FanoutS3Config.FlushInterval": {},
	"FanoutGCSConfig.Bucket":       {}, "FanoutGCSConfig.Prefix": {},
	"FanoutGCSConfig.Schema": {}, "FanoutGCSConfig.BatchSize": {},
	"FanoutGCSConfig.FlushInterval":     {},
	"FanoutDeliveryPolicyConfig.Policy": {}, "FanoutDeliveryPolicyConfig.Fallback": {},
	"FanoutDeliveryPolicyConfig.DLQ": {},
	"FanoutFallbackConfig.Enabled":   {}, "FanoutFallbackConfig.OnPrimaryFailure": {},
	"FanoutFallbackConfig.OnSecondaryFailure": {}, "FanoutFallbackConfig.OnPolicyFailure": {},
	"FanoutDLQConfig.OnPrimaryFailure": {}, "FanoutDLQConfig.OnSecondaryFailure": {},
	"FanoutDLQConfig.OnFallbackFailure": {}, "FanoutDLQConfig.OnPolicyFailure": {},

	// Dedupe (Key / RedisPassword are secrets).
	"DedupeConfig.Enabled": {}, "DedupeConfig.Window": {},
	"DedupeConfig.Backend": {}, "DedupeConfig.RedisAddr": {},
	"DedupeConfig.RedisDB": {}, "DedupeConfig.RedisPrefix": {},

	// Schema governance, cortex bridge (APIKey is a secret), event bus.
	"SchemaGovernanceConfig.Mode": {}, "SchemaGovernanceConfig.SchemaVersion": {},
	"SchemaGovernanceConfig.EventVersion": {}, "SchemaGovernanceConfig.RegistryFile": {},
	"SchemaGovernanceConfig.Registry": {}, "SchemaGovernanceConfig.QuarantinePath": {},
	"SchemaRegistryEntry.SchemaVersion": {}, "SchemaRegistryEntry.EventVersion": {},
	"SchemaRegistryEntry.RequiredFields": {},
	"CortexBridgeConfig.Enabled":         {}, "CortexBridgeConfig.Mode": {},
	"CortexBridgeConfig.Endpoint": {}, "CortexBridgeConfig.Insecure": {},
	"CortexBridgeConfig.Timeout": {}, "CortexBridgeConfig.BatchSize": {},
	"CortexBridgeConfig.FlushInterval": {}, "CortexBridgeConfig.QueueSize": {},
	"CortexBridgeConfig.Header": {},
	"EventBusConfig.Type":       {}, "EventBusConfig.Topic": {}, "EventBusConfig.DLQTopic": {},
	"EventBusConfig.ConsumerGroup": {}, "EventBusConfig.Memory": {},
	"EventBusConfig.Redis": {}, "EventBusConfig.NATS": {}, "EventBusConfig.Kafka": {},
	"EBMemoryConfig.BufferSize": {},
	"EBRedisConfig.Addr":        {}, "EBRedisConfig.DB": {}, "EBRedisConfig.Stream": {},
	"EBRedisConfig.Group": {}, "EBRedisConfig.MaxLen": {},
	"EBNATSConfig.URL": {}, "EBNATSConfig.Stream": {}, "EBNATSConfig.Subject": {},
	"EBNATSConfig.Durable": {}, "EBNATSConfig.Username": {},
	"EBKafkaConfig.Brokers": {}, "EBKafkaConfig.Topic": {},
	"EBKafkaConfig.ConsumerGroup": {}, "EBKafkaConfig.Acks": {},
	"EBKafkaConfig.EnableIdempotence": {}, "EBKafkaConfig.MaxRetries": {},
	"EBKafkaConfig.Compression": {},
}

var (
	durationType = reflect.TypeOf(time.Duration(0))
	timeType     = reflect.TypeOf(time.Time{})
)

// Printable returns a deep copy of c in which every field that is not in
// printableAllowlist is replaced with RedactedMarker. The returned value has
// the same type as c, so YAML/JSON encoding preserves its original shape and
// field names while never emitting a secret value.
func (c Config) Printable() Config {
	return redactCopy(reflect.ValueOf(c)).Interface().(Config)
}

func redactCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Elem().Type())
		out.Elem().Set(redactCopy(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(redactCopy(v.Elem()))
		return out
	case reflect.Struct:
		if v.Type() == timeType || v.Type() == durationType {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" { // unexported
				continue
			}
			key := t.Name() + "." + field.Name
			if _, ok := printableAllowlist[key]; !ok {
				out.Field(i).Set(redactedValue(field.Type))
				continue
			}
			out.Field(i).Set(redactCopy(v.Field(i)))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(redactCopy(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(redactCopy(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), redactCopy(iter.Value()))
		}
		return out
	default:
		return v
	}
}

// redactedValue returns the value used in place of a non-allowlisted field.
// Secret fields are strings, so they become RedactedMarker; any other type is
// zeroed so that no nested data can survive redaction.
func redactedValue(t reflect.Type) reflect.Value {
	if t.Kind() == reflect.String {
		return reflect.ValueOf(RedactedMarker).Convert(t)
	}
	return reflect.Zero(t)
}
