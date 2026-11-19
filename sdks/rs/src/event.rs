use crate::errors::LozaError;
pub use crate::generated::spec_contract::{LOZA_EVENT_VERSION, LOZA_SPEC_VERSION};
use serde::Serialize;
use serde_json::{Map, Value};
use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use time::format_description::well_known::Rfc3339;
use time::OffsetDateTime;

pub const EVENT_CREATED: &str = "created";
pub const EVENT_ACTIVE: &str = "active";
pub const EVENT_FINISHED: &str = "finished";
pub const EVENT_EMITTING: &str = "emitting";
pub const EVENT_EMITTED: &str = "emitted";
pub const EVENT_FAILED_VALIDATION: &str = "failed_validation";
pub const EVENT_DELIVERY_FAILED: &str = "delivery_failed";

#[derive(Clone, Debug)]
pub struct LifecycleState {
    pub state: String,
    pub emitted: bool,
    pub emit_attempted: bool,
    pub emitted_payload: Option<String>,
}

#[derive(Clone, Debug)]
pub struct Attr {
    pub key: String,
    pub value: Value,
    pub sensitive: bool,
    pub hash_value: bool,
}

impl Attr {
    pub fn new(key: impl Into<String>, value: impl Into<Value>) -> Self {
        Self {
            key: key.into(),
            value: value.into(),
            sensitive: false,
            hash_value: false,
        }
    }

    pub fn sensitive(mut self) -> Self {
        self.sensitive = true;
        self
    }

    pub fn hash_value(mut self) -> Self {
        self.hash_value = true;
        self
    }
}

#[derive(Clone, Debug)]
pub struct Params {
    pub event: String,
    pub kind: String,
    pub message: Option<String>,
    pub level: String,
    pub method: Option<String>,
    pub path: Option<String>,
    pub route: Option<String>,
    pub status_code: Option<u16>,
    pub service: Option<String>,
    pub version: Option<String>,
    pub environment: Option<String>,
    pub region: Option<String>,
    pub deployment_id: Option<String>,
    pub request_id: Option<String>,
    pub trace_id: Option<String>,
    pub span_id: Option<String>,
    pub incident_id: Option<String>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct ContextCarrier {
    pub trace_id: Option<String>,
    pub span_id: Option<String>,
    pub tracestate: Option<String>,
    pub request_id: Option<String>,
    pub incident_id: Option<String>,
    pub baggage: BTreeMap<String, String>,
    /// W3C traceparent version captured from an incoming header. `None` falls
    /// back to `00` for carriers built locally.
    pub traceparent_version: Option<String>,
    /// W3C traceparent trace-flags captured from an incoming header. `None`
    /// falls back to `01` (sampled) for carriers built locally.
    pub traceparent_flags: Option<String>,
}

pub trait ContextSource {
    fn inherit_params(&self, params: Params) -> Params;
}

impl Params {
    pub fn new(event: impl Into<String>) -> Self {
        Self {
            event: event.into(),
            kind: "event".to_string(),
            message: None,
            level: "info".to_string(),
            method: None,
            path: None,
            route: None,
            status_code: None,
            service: None,
            version: None,
            environment: None,
            region: None,
            deployment_id: None,
            request_id: None,
            trace_id: None,
            span_id: None,
            incident_id: None,
        }
    }

    pub fn with_kind(mut self, kind: impl Into<String>) -> Self {
        self.kind = kind.into();
        self
    }

    pub fn with_message(mut self, message: impl Into<String>) -> Self {
        self.message = Some(message.into());
        self
    }

    pub fn with_method(mut self, method: impl Into<String>) -> Self {
        self.method = Some(method.into());
        self
    }

    pub fn with_path(mut self, path: impl Into<String>) -> Self {
        self.path = Some(path.into());
        self
    }

    pub fn with_route(mut self, route: impl Into<String>) -> Self {
        self.route = Some(route.into());
        self
    }

    pub fn with_status_code(mut self, status_code: u16) -> Self {
        self.status_code = Some(status_code);
        self
    }

    pub fn with_incident_id(mut self, incident_id: impl Into<String>) -> Self {
        self.incident_id = Some(incident_id.into());
        self
    }

    pub fn inherit_from(mut self, parent: &EventContext) -> Self {
        if self.service.is_none() {
            self.service = Some(parent.service.clone());
        }
        if self.request_id.is_none() {
            self.request_id = Some(parent.request_id.clone());
        }
        if self.trace_id.is_none() {
            self.trace_id = parent.trace_id.clone();
        }
        if self.span_id.is_none() {
            self.span_id = parent.span_id.clone();
        }
        if self.incident_id.is_none() {
            self.incident_id = parent.incident_id.clone();
        }
        self
    }

    pub fn inherit_from_carrier(mut self, carrier: &ContextCarrier) -> Self {
        if self.request_id.is_none() {
            self.request_id = carrier.request_id.clone();
        }
        if self.trace_id.is_none() {
            self.trace_id = carrier.trace_id.clone();
        }
        if self.span_id.is_none() {
            self.span_id = carrier.span_id.clone();
        }
        if self.incident_id.is_none() {
            self.incident_id = carrier.incident_id.clone();
        }
        self
    }
}

impl ContextCarrier {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn with_trace_id(mut self, trace_id: impl Into<String>) -> Self {
        self.trace_id = Some(trace_id.into());
        self
    }

    pub fn with_span_id(mut self, span_id: impl Into<String>) -> Self {
        self.span_id = Some(span_id.into());
        self
    }

    pub fn with_tracestate(mut self, tracestate: impl Into<String>) -> Self {
        self.tracestate = Some(tracestate.into());
        self
    }

    pub fn with_request_id(mut self, request_id: impl Into<String>) -> Self {
        self.request_id = Some(request_id.into());
        self
    }

    pub fn with_incident_id(mut self, incident_id: impl Into<String>) -> Self {
        self.incident_id = Some(incident_id.into());
        self
    }

    pub fn with_baggage(mut self, key: impl Into<String>, value: impl Into<String>) -> Self {
        self.baggage.insert(key.into(), value.into());
        self
    }

    pub fn with_traceparent_version(mut self, version: impl Into<String>) -> Self {
        self.traceparent_version = Some(version.into());
        self
    }

    pub fn with_traceparent_flags(mut self, flags: impl Into<String>) -> Self {
        self.traceparent_flags = Some(flags.into());
        self
    }

    pub fn from_traceparent(traceparent: &str) -> Option<Self> {
        let mut parts = traceparent.trim().split('-');
        let version = parts.next()?;
        let trace_id = parts.next()?;
        let span_id = parts.next()?;
        let flags = parts.next()?;
        if parts.next().is_some() {
            return None;
        }
        if version.len() != 2
            || trace_id.len() != 32
            || span_id.len() != 16
            || flags.len() != 2
            || !trace_id.chars().all(|ch| ch.is_ascii_hexdigit())
            || !span_id.chars().all(|ch| ch.is_ascii_hexdigit())
            || !version.chars().all(|ch| ch.is_ascii_hexdigit())
            || !flags.chars().all(|ch| ch.is_ascii_hexdigit())
        {
            return None;
        }
        // W3C Trace Context reserves version `ff` and forbids all-zero
        // trace-id / span-id values; such headers are spec-invalid.
        if version.eq_ignore_ascii_case("ff")
            || trace_id.bytes().all(|byte| byte == b'0')
            || span_id.bytes().all(|byte| byte == b'0')
        // W3C Trace Context: `ff` is a reserved/invalid version, and the
        // all-zero trace-id and span-id are explicitly invalid.
        if version.eq_ignore_ascii_case("ff")
            || trace_id.bytes().all(|b| b == b'0')
            || span_id.bytes().all(|b| b == b'0')
        {
            return None;
        }
        Some(
            Self::new()
                .with_trace_id(trace_id.to_ascii_lowercase())
                .with_span_id(span_id.to_ascii_lowercase())
                .with_traceparent_version(version.to_ascii_lowercase())
                .with_traceparent_flags(flags.to_ascii_lowercase()),
        )
    }

    pub fn traceparent(&self) -> Option<String> {
        let trace_id = self.trace_id.as_deref()?;
        let span_id = self.span_id.as_deref()?;
        if trace_id.len() != 32
            || span_id.len() != 16
            || !trace_id.chars().all(|ch| ch.is_ascii_hexdigit())
            || !span_id.chars().all(|ch| ch.is_ascii_hexdigit())
        {
            return None;
        }
        let version = self.traceparent_version.as_deref().unwrap_or("00");
        let flags = self.traceparent_flags.as_deref().unwrap_or("01");
        if version.len() != 2
            || flags.len() != 2
            || !version.chars().all(|ch| ch.is_ascii_hexdigit())
            || !flags.chars().all(|ch| ch.is_ascii_hexdigit())
        {
            return None;
        }
        Some(format!(
            "{}-{}-{}-{}",
            version.to_ascii_lowercase(),
            trace_id.to_ascii_lowercase(),
            span_id.to_ascii_lowercase(),
            flags.to_ascii_lowercase()
        ))
    }
}

impl ContextSource for EventContext {
    fn inherit_params(&self, params: Params) -> Params {
        params.inherit_from(self)
    }
}

impl ContextSource for ContextCarrier {
    fn inherit_params(&self, params: Params) -> Params {
        params.inherit_from_carrier(self)
    }
}

#[derive(Clone, Debug, Serialize, serde::Deserialize)]
pub struct EventContext {
    pub timestamp: String,
    pub schema_version: String,
    pub event_version: String,
    pub event_id: String,
    pub request_id: String,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub trace_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub span_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub incident_id: Option<String>,
    pub service: String,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub version: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub environment: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub region: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub deployment_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub host: Option<String>,
    pub event: String,
    pub kind: String,
    pub level: String,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub message: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub outcome: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub duration_ms: Option<u128>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub method: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub path: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub route: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub status_code: Option<u16>,
    #[serde(skip_serializing_if = "Map::is_empty", default)]
    pub http: Map<String, Value>,
    #[serde(skip_serializing_if = "Map::is_empty", default)]
    pub user: Map<String, Value>,
    #[serde(skip_serializing_if = "Map::is_empty", default)]
    pub tenant: Map<String, Value>,
    #[serde(skip_serializing_if = "Map::is_empty", default)]
    pub resource: Map<String, Value>,
    #[serde(skip_serializing_if = "Map::is_empty", default)]
    pub attrs: Map<String, Value>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub error: Option<Map<String, Value>>,
    #[serde(skip_serializing_if = "Vec::is_empty", default)]
    pub checkpoints: Vec<Map<String, Value>>,
    #[serde(skip_serializing_if = "Vec::is_empty", default)]
    pub processes: Vec<Map<String, Value>>,
    #[serde(skip_serializing_if = "Vec::is_empty", default)]
    pub groups: Vec<Map<String, Value>>,
    #[serde(skip_serializing_if = "Vec::is_empty", default)]
    pub timers: Vec<Map<String, Value>>,
    #[serde(skip_serializing_if = "Vec::is_empty", default)]
    pub links: Vec<Map<String, Value>>,
    #[serde(skip, default)]
    process_step: u32,
    #[serde(skip_serializing_if = "std::ops::Not::not", default)]
    pub partial: bool,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub partial_reason: Option<String>,
    pub event_state: String,
    #[serde(skip_serializing_if = "is_zero_u32", default)]
    pub delivery_attempts: u32,
    #[serde(skip, default)]
    started_unix_ms: u128,
    #[serde(skip, default)]
    emitted: bool,
    #[serde(skip, default = "default_lifecycle")]
    lifecycle: Arc<Mutex<LifecycleState>>,
    #[serde(skip, default)]
    pending_error: Option<String>,
    #[serde(skip, default)]
    pub(crate) sensitive_keys: Vec<String>,
    #[serde(skip, default)]
    pub(crate) hash_keys: Vec<String>,
}

impl EventContext {
    pub fn new(service: impl Into<String>, params: Params) -> Self {
        let started_unix_ms = now_unix_ms();
        let timestamp = OffsetDateTime::now_utc()
            .format(&Rfc3339)
            .unwrap_or_else(|_| "1970-01-01T00:00:00Z".to_string());
        let event_id = crate::internal::core::uuidv7::new_event_id();
        let request_id = params
            .request_id
            .clone()
            .unwrap_or_else(crate::internal::core::uuidv7::new_request_id);
        Self {
            timestamp,
            schema_version: LOZA_SPEC_VERSION.to_string(),
            event_version: LOZA_EVENT_VERSION.to_string(),
            event_id,
            request_id,
            trace_id: params.trace_id,
            span_id: params.span_id,
            incident_id: params.incident_id,
            service: params.service.clone().unwrap_or_else(|| service.into()),
            version: params.version,
            environment: params.environment,
            region: params.region,
            deployment_id: params.deployment_id,
            host: None,
            event: params.event,
            kind: params.kind,
            level: params.level,
            message: params.message,
            outcome: None,
            duration_ms: None,
            method: params.method,
            path: params.path,
            route: params.route,
            status_code: params.status_code,
            http: Map::new(),
            user: Map::new(),
            tenant: Map::new(),
            resource: Map::new(),
            attrs: Map::new(),
            error: None,
            checkpoints: Vec::new(),
            processes: Vec::new(),
            groups: Vec::new(),
            timers: Vec::new(),
            links: Vec::new(),
            process_step: 0,
            partial: false,
            partial_reason: None,
            event_state: EVENT_CREATED.to_string(),
            delivery_attempts: 0,
            started_unix_ms,
            emitted: false,
            lifecycle: Arc::new(Mutex::new(LifecycleState {
                state: EVENT_CREATED.to_string(),
                emitted: false,
                emit_attempted: false,
                emitted_payload: None,
            })),
            pending_error: None,
            sensitive_keys: Vec::new(),
            hash_keys: Vec::new(),
        }
    }

    pub(crate) fn ensure_mutable(&self) -> Result<(), LozaError> {
        let state = self.lifecycle_state();
        match state.as_str() {
            EVENT_CREATED | EVENT_ACTIVE | EVENT_FINISHED => Ok(()),
            _ => Err(LozaError::EventClosed {
                event_id: self.event_id.clone(),
                state,
            }),
        }
    }

    pub fn set_attr(&mut self, key: impl Into<String>, value: impl Into<Value>) {
        if self.ensure_mutable().is_err() {
            return;
        }
        self.mark_active_if_created();
        let key = key.into();
        let value = value.into();
        if let Some(child) = key.strip_prefix("user.") {
            set_nested(&mut self.user, child, value);
        } else if let Some(child) = key.strip_prefix("tenant.") {
            set_nested(&mut self.tenant, child, value);
        } else if let Some(child) = key.strip_prefix("http.") {
            set_nested(&mut self.http, child, value);
        } else if let Some(child) = key.strip_prefix("resource.") {
            set_nested(&mut self.resource, child, value);
        } else {
            set_nested(&mut self.attrs, &key, value);
        }
    }

    pub fn append_attr(&mut self, attr: Attr) {
        if self.ensure_mutable().is_err() {
            return;
        }
        self.mark_active_if_created();
        if attr.sensitive {
            self.sensitive_keys.push(attr.key.clone());
        }
        if attr.hash_value {
            self.hash_keys.push(attr.key.clone());
        }
        if let Some(key) = attr.key.strip_prefix("user.") {
            self.user.insert(key.to_string(), attr.value);
        } else if let Some(key) = attr.key.strip_prefix("tenant.") {
            self.tenant.insert(key.to_string(), attr.value);
        } else if let Some(key) = attr.key.strip_prefix("http.") {
            self.http.insert(key.to_string(), attr.value);
        } else if let Some(key) = attr.key.strip_prefix("resource.") {
            self.resource.insert(key.to_string(), attr.value);
        } else {
            set_nested(&mut self.attrs, &attr.key, attr.value);
        }
    }

    pub fn merge_group(&mut self, group: &str, key: impl Into<String>, value: impl Into<Value>) {
        if self.ensure_mutable().is_err() {
            return;
        }
        self.mark_active_if_created();
        let target = match group {
            "http" => &mut self.http,
            "user" => &mut self.user,
            "tenant" => &mut self.tenant,
            "resource" => &mut self.resource,
            _ => &mut self.attrs,
        };
        target.insert(key.into(), value.into());
    }

    pub fn delete_attr(&mut self, key: &str) {
        if self.ensure_mutable().is_err() {
            return;
        }
        self.mark_active_if_created();
        if let Some(child) = key.strip_prefix("user.") {
            remove_nested(&mut self.user, child);
        } else if let Some(child) = key.strip_prefix("tenant.") {
            remove_nested(&mut self.tenant, child);
        } else if let Some(child) = key.strip_prefix("http.") {
            remove_nested(&mut self.http, child);
        } else if let Some(child) = key.strip_prefix("resource.") {
            remove_nested(&mut self.resource, child);
        } else {
            remove_nested(&mut self.attrs, key);
        }
    }

    pub fn checkpoint(&mut self, name: &str) {
        self.checkpoint_with_attrs(name, &[]);
    }

    pub fn checkpoint_with_attrs(&mut self, name: &str, attrs: &[Attr]) {
        if self.ensure_mutable().is_err() {
            return;
        }
        self.mark_active_if_created();
        let mut checkpoint = Map::new();
        checkpoint.insert("name".to_string(), Value::String(name.to_string()));
        checkpoint.insert(
            "at_ms".to_string(),
            Value::Number(serde_json::Number::from(
                now_unix_ms().saturating_sub(self.started_unix_ms) as u64,
            )),
        );
        for attr in attrs {
            checkpoint.insert(attr.key.clone(), attr.value.clone());
        }
        self.checkpoints.push(checkpoint);
    }

    /// Start a named process step and return a handle to finish it.
    pub fn start_process(&mut self, name: &str) -> ProcessHandle {
        let mutable = self.ensure_mutable().is_ok();
        if mutable {
            self.mark_active_if_created();
            self.process_step += 1;
        }
        ProcessHandle {
            name: name.to_string(),
            step: self.process_step,
            started_at_instant: std::time::Instant::now(),
            started_at_ms: now_unix_ms().saturating_sub(self.started_unix_ms) as u64,
        }
    }

    /// Start a named timer and return a handle to stop it.
    pub fn start_timer(&mut self, name: &str) -> TimerHandle {
        let mutable = self.ensure_mutable().is_ok();
        if mutable {
            self.mark_active_if_created();
        }
        TimerHandle {
            name: name.to_string(),
            started_at_instant: std::time::Instant::now(),
        }
    }

    /// Start a named group phase and return a handle to finish it.
    pub fn start_group(&mut self, name: &str) -> GroupHandle {
        let mutable = self.ensure_mutable().is_ok();
        if mutable {
            self.mark_active_if_created();
        }
        GroupHandle {
            name: name.to_string(),
            started_at_instant: std::time::Instant::now(),
            started_at_ms: now_unix_ms().saturating_sub(self.started_unix_ms) as u64,
        }
    }

    pub fn finish(&mut self, outcome: impl Into<String>) -> Result<(), LozaError> {
        self.ensure_mutable()?;
        if self.outcome.is_some() {
            return Err(LozaError::EventAlreadyFinished {
                event_id: self.event_id.clone(),
            });
        }
        self.outcome = Some(outcome.into());
        self.duration_ms = Some(now_unix_ms().saturating_sub(self.started_unix_ms));
        self.event_state = EVENT_FINISHED.to_string();
        self.set_lifecycle(EVENT_FINISHED, false, false);
        Ok(())
    }

    pub fn finish_error(&mut self, message: impl Into<String>) -> Result<(), LozaError> {
        self.finish("error")?;
        let mut error = Map::new();
        error.insert("type".to_string(), Value::String("error".to_string()));
        error.insert("message".to_string(), Value::String(message.into()));
        self.error = Some(error);
        self.level = "error".to_string();
        Ok(())
    }

    pub fn mark_emitted(&mut self) {
        self.emitted = true;
        self.event_state = EVENT_EMITTED.to_string();
        self.set_lifecycle(EVENT_EMITTED, true, true);
    }

    pub fn is_emitted(&self) -> bool {
        self.lifecycle
            .lock()
            .map(|state| state.emitted)
            .unwrap_or(self.emitted)
    }

    pub fn is_finished(&self) -> bool {
        self.outcome.is_some()
    }

    pub fn lifecycle_state(&self) -> String {
        self.lifecycle
            .lock()
            .map(|state| state.state.clone())
            .unwrap_or_else(|_| self.event_state.clone())
    }

    pub fn begin_emit(&self) -> Result<(), LozaError> {
        let mut lifecycle = self.lifecycle.lock().map_err(|_| LozaError::EventClosed {
            event_id: self.event_id.clone(),
            state: "poisoned".to_string(),
        })?;
        match lifecycle.state.as_str() {
            EVENT_EMITTED => Err(LozaError::DuplicateEmit {
                event_id: self.event_id.clone(),
            }),
            EVENT_EMITTING | EVENT_DELIVERY_FAILED | EVENT_FAILED_VALIDATION => {
                Err(LozaError::EventClosed {
                    event_id: self.event_id.clone(),
                    state: lifecycle.state.clone(),
                })
            }
            _ => {
                lifecycle.state = EVENT_EMITTING.to_string();
                lifecycle.emit_attempted = true;
                Ok(())
            }
        }
    }

    pub fn mark_validation_failed(&self) {
        self.set_lifecycle(EVENT_FAILED_VALIDATION, false, true);
    }

    pub fn mark_delivery_failed(&self) {
        self.set_lifecycle(EVENT_DELIVERY_FAILED, false, true);
    }

    pub fn mark_delivery_accepted(&self, payload: Option<&str>) {
        self.set_lifecycle(EVENT_EMITTED, true, true);
        if let Ok(mut lifecycle) = self.lifecycle.lock() {
            lifecycle.emitted_payload = payload.map(ToString::to_string);
        }
    }

    pub fn cached_emitted_payload(&self) -> Option<String> {
        self.lifecycle
            .lock()
            .ok()
            .and_then(|state| state.emitted_payload.clone())
    }

    pub fn note_error(&mut self, message: impl Into<String>) {
        if self.pending_error.is_none() {
            self.pending_error = Some(message.into());
        }
    }

    pub fn pending_error(&self) -> Option<String> {
        self.pending_error.clone()
    }

    fn set_lifecycle(&self, state: &str, emitted: bool, emit_attempted: bool) {
        if let Ok(mut lifecycle) = self.lifecycle.lock() {
            lifecycle.state = state.to_string();
            lifecycle.emitted = emitted;
            lifecycle.emit_attempted = emit_attempted;
            if state != EVENT_EMITTED {
                lifecycle.emitted_payload = None;
            }
        }
    }

    pub(crate) fn mark_active_if_created(&mut self) {
        if self.event_state == EVENT_CREATED {
            self.event_state = EVENT_ACTIVE.to_string();
            if let Ok(mut lifecycle) = self.lifecycle.lock() {
                if lifecycle.state == EVENT_CREATED {
                    lifecycle.state = EVENT_ACTIVE.to_string();
                }
            }
        }
    }

    #[allow(dead_code)]
    pub(crate) fn apply_sensitive_flags(&mut self) {
        for key in &self.sensitive_keys {
            let k = key.clone();
            redact_in_map(&mut self.attrs, &k);
            redact_in_map(&mut self.user, &k);
            redact_in_map(&mut self.tenant, &k);
            redact_in_map(&mut self.http, &k);
            redact_in_map(&mut self.resource, &k);
        }
        for key in &self.hash_keys {
            let k = key.clone();
            hash_in_map(&mut self.attrs, &k);
            hash_in_map(&mut self.user, &k);
            hash_in_map(&mut self.tenant, &k);
            hash_in_map(&mut self.http, &k);
            hash_in_map(&mut self.resource, &k);
        }
    }
}

fn redact_in_map(map: &mut Map<String, Value>, key: &str) {
    if let Some(parts) = parse_dotted_key(key) {
        redact_nested(map, &parts);
    } else if let Some(val) = map.get_mut(key) {
        *val = Value::String("[REDACTED]".to_string());
    }
}

fn hash_in_map(map: &mut Map<String, Value>, key: &str) {
    use sha2::{Digest, Sha256};
    if let Some(parts) = parse_dotted_key(key) {
        hash_nested(map, &parts);
    } else if let Some(val) = map.get_mut(key) {
        if let Some(s) = val.as_str() {
            let mut hasher = Sha256::new();
            hasher.update(s.as_bytes());
            *val = Value::String(format!("{:x}", hasher.finalize()));
        }
    }
}

fn parse_dotted_key(key: &str) -> Option<Vec<String>> {
    if key.contains('.') {
        Some(key.split('.').map(|s| s.to_string()).collect())
    } else {
        None
    }
}

fn redact_nested(map: &mut Map<String, Value>, parts: &[String]) {
    if parts.len() == 1 {
        if let Some(val) = map.get_mut(&parts[0]) {
            *val = Value::String("[REDACTED]".to_string());
        }
    } else if let Some(Value::Object(child)) = map.get_mut(&parts[0]) {
        redact_nested(child, &parts[1..]);
    }
}

fn hash_nested(map: &mut Map<String, Value>, parts: &[String]) {
    use sha2::{Digest, Sha256};
    if parts.len() == 1 {
        if let Some(val) = map.get_mut(&parts[0]) {
            if let Some(s) = val.as_str() {
                let mut hasher = Sha256::new();
                hasher.update(s.as_bytes());
                *val = Value::String(format!("{:x}", hasher.finalize()));
            }
        }
    } else if let Some(Value::Object(child)) = map.get_mut(&parts[0]) {
        hash_nested(child, &parts[1..]);
    }
}

fn is_zero_u32(value: &u32) -> bool {
    *value == 0
}

fn set_nested(target: &mut Map<String, Value>, key: &str, value: Value) {
    if !key.contains('.') {
        target.insert(key.to_string(), value);
        return;
    }
    let mut current = target;
    let mut parts = key.split('.').peekable();
    while let Some(part) = parts.next() {
        if parts.peek().is_none() {
            current.insert(part.to_string(), value);
            return;
        }
        let entry = current
            .entry(part.to_string())
            .or_insert_with(|| Value::Object(Map::new()));
        if !entry.is_object() {
            *entry = Value::Object(Map::new());
        }
        current = entry.as_object_mut().expect("object");
    }
}

fn remove_nested(target: &mut Map<String, Value>, key: &str) {
    let mut current = target;
    let mut parts = key.split('.').peekable();
    while let Some(part) = parts.next() {
        if parts.peek().is_none() {
            current.remove(part);
            return;
        }
        let Some(entry) = current.get_mut(part) else {
            return;
        };
        let Some(child) = entry.as_object_mut() else {
            return;
        };
        current = child;
    }
}

fn default_lifecycle() -> Arc<Mutex<LifecycleState>> {
    Arc::new(Mutex::new(LifecycleState {
        state: String::new(),
        emitted: false,
        emit_attempted: false,
        emitted_payload: None,
    }))
}

fn now_unix_ms() -> u128 {
    crate::internal::clock::unix_millis()
}

/// Apply sensitive/hash flags from an EventContext onto a serialized Value.
/// This is used during emit when we can't mutate the original EventContext.
pub fn apply_sensitive_to_value(mut value: Value, ctx: &EventContext) -> Value {
    if ctx.sensitive_keys.is_empty() && ctx.hash_keys.is_empty() {
        return value;
    }
    let obj = match value.as_object_mut() {
        Some(o) => o,
        None => return value,
    };
    for key in &ctx.sensitive_keys {
        apply_redact_dotted_or_flat(obj, key);
    }
    for key in &ctx.hash_keys {
        apply_hash_dotted_or_flat(obj, key);
    }
    value
}

fn apply_redact_dotted_or_flat(obj: &mut Map<String, Value>, key: &str) {
    let sub_map_prefixes = ["user", "tenant", "http", "resource"];
    if let Some(parts) = parse_dotted_key(key) {
        if sub_map_prefixes.contains(&parts[0].as_str()) {
            if let Some(Value::Object(map)) = obj.get_mut(&parts[0]) {
                redact_nested(map, &parts[1..]);
            }
        }
        if let Some(Value::Object(map)) = obj.get_mut("attrs") {
            redact_nested(map, &parts);
        }
    } else {
        for field in &["attrs", "user", "tenant", "http", "resource"] {
            if let Some(Value::Object(map)) = obj.get_mut(*field) {
                redact_in_map(map, key);
            }
        }
    }
}

/// Handle for a named process step with automatic duration tracking.
pub struct ProcessHandle {
    name: String,
    step: u32,
    started_at_instant: std::time::Instant,
    started_at_ms: u64,
}

impl ProcessHandle {
    /// Finish the process with optional attrs.
    pub fn finish(self, event: &mut EventContext, attrs: &[Attr]) {
        let ended_ms = now_unix_ms().saturating_sub(event.started_unix_ms) as u64;
        let mut entry = Map::new();
        entry.insert("step".to_string(), Value::Number(self.step.into()));
        entry.insert("name".to_string(), Value::String(self.name));
        entry.insert(
            "started_at_ms".to_string(),
            Value::Number(self.started_at_ms.into()),
        );
        entry.insert("ended_at_ms".to_string(), Value::Number(ended_ms.into()));
        entry.insert(
            "duration_ms".to_string(),
            Value::Number(ended_ms.saturating_sub(self.started_at_ms).into()),
        );
        for attr in attrs {
            if attr.key == "status_code" {
                entry.insert("status_code".to_string(), attr.value.clone());
            } else {
                entry.insert(attr.key.clone(), attr.value.clone());
            }
        }
        event.processes.push(entry);
    }

    /// Finish the process with an error.
    pub fn finish_error(self, event: &mut EventContext, message: &str, attrs: &[Attr]) {
        let mut all_attrs = attrs.to_vec();
        all_attrs.push(Attr::new("error_message", message));
        self.finish(event, &all_attrs);
    }

    /// Get elapsed duration since the process started.
    pub fn duration(&self) -> std::time::Duration {
        self.started_at_instant.elapsed()
    }
}

/// Handle for a named timer with automatic duration tracking.
pub struct TimerHandle {
    name: String,
    started_at_instant: std::time::Instant,
}

impl TimerHandle {
    /// Stop the timer with optional attrs.
    pub fn stop(self, event: &mut EventContext, attrs: &[Attr]) {
        let duration_ms = self.started_at_instant.elapsed().as_millis() as u64;
        let mut entry = Map::new();
        entry.insert("name".to_string(), Value::String(self.name));
        entry.insert("duration_ms".to_string(), Value::Number(duration_ms.into()));
        for attr in attrs {
            if attr.key == "status_code" {
                entry.insert("status_code".to_string(), attr.value.clone());
            } else {
                entry.insert(attr.key.clone(), attr.value.clone());
            }
        }
        event.timers.push(entry);
    }

    /// Get elapsed duration since the timer started.
    pub fn duration(&self) -> std::time::Duration {
        self.started_at_instant.elapsed()
    }
}

/// Handle for a named group phase with automatic duration tracking.
pub struct GroupHandle {
    name: String,
    started_at_instant: std::time::Instant,
    started_at_ms: u64,
}

impl GroupHandle {
    /// Finish the group with optional attrs.
    pub fn finish(self, event: &mut EventContext, attrs: &[Attr]) {
        let ended_ms = now_unix_ms().saturating_sub(event.started_unix_ms) as u64;
        let mut entry = Map::new();
        entry.insert("name".to_string(), Value::String(self.name));
        entry.insert(
            "started_at_ms".to_string(),
            Value::Number(self.started_at_ms.into()),
        );
        entry.insert("ended_at_ms".to_string(), Value::Number(ended_ms.into()));
        entry.insert(
            "duration_ms".to_string(),
            Value::Number(ended_ms.saturating_sub(self.started_at_ms).into()),
        );
        for attr in attrs {
            if attr.key == "status_code" {
                entry.insert("status_code".to_string(), attr.value.clone());
            } else {
                entry.insert(attr.key.clone(), attr.value.clone());
            }
        }
        event.groups.push(entry);
    }

    /// Get elapsed duration since the group started.
    pub fn duration(&self) -> std::time::Duration {
        self.started_at_instant.elapsed()
    }
}

/// Standalone elapsed-time measurer with no event reference.
pub struct StopwatchHandle {
    started_at: std::time::Instant,
}

impl Default for StopwatchHandle {
    fn default() -> Self {
        Self::new()
    }
}

impl StopwatchHandle {
    /// Create a new stopwatch.
    pub fn new() -> Self {
        Self {
            started_at: std::time::Instant::now(),
        }
    }

    /// Get elapsed duration since the stopwatch was created.
    pub fn elapsed(&self) -> std::time::Duration {
        self.started_at.elapsed()
    }
}

fn apply_hash_dotted_or_flat(obj: &mut Map<String, Value>, key: &str) {
    let sub_map_prefixes = ["user", "tenant", "http", "resource"];
    if let Some(parts) = parse_dotted_key(key) {
        if sub_map_prefixes.contains(&parts[0].as_str()) {
            if let Some(Value::Object(map)) = obj.get_mut(&parts[0]) {
                hash_nested(map, &parts[1..]);
            }
        }
        if let Some(Value::Object(map)) = obj.get_mut("attrs") {
            hash_nested(map, &parts);
        }
    } else {
        for field in &["attrs", "user", "tenant", "http", "resource"] {
            if let Some(Value::Object(map)) = obj.get_mut(*field) {
                hash_in_map(map, key);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Regression test for MASS-0756: finishing a handle after a backward clock
    /// adjustment (or against a different event) used to subtract a larger
    /// `started_at_ms` from a smaller `ended_ms`, panicking in debug builds and
    /// wrapping to a huge value in release builds. The duration must saturate
    /// at zero instead.
    #[test]
    fn process_finish_duration_saturates_when_end_precedes_start() {
        let mut event = EventContext::new("test", Params::new("test"));
        let handle = ProcessHandle {
            name: "backwards".to_string(),
            step: 1,
            started_at_instant: std::time::Instant::now(),
            started_at_ms: u64::MAX,
        };

        handle.finish(&mut event, &[]);

        let duration = event.processes[0]
            .get("duration_ms")
            .and_then(|v| v.as_u64())
            .expect("duration_ms must be present");
        assert_eq!(duration, 0, "duration must saturate to zero, not underflow");
    }

    #[test]
    fn group_finish_duration_saturates_when_end_precedes_start() {
        let mut event = EventContext::new("test", Params::new("test"));
        let handle = GroupHandle {
            name: "backwards".to_string(),
            started_at_instant: std::time::Instant::now(),
            started_at_ms: u64::MAX,
        };

        handle.finish(&mut event, &[]);

        let duration = event.groups[0]
            .get("duration_ms")
            .and_then(|v| v.as_u64())
            .expect("duration_ms must be present");
        assert_eq!(duration, 0, "duration must saturate to zero, not underflow");
    }

    #[test]
    fn process_finish_duration_still_normal_when_end_follows_start() {
        let mut event = EventContext::new("test", Params::new("test"));
        let handle = event.start_process("normal");
        handle.finish(&mut event, &[]);

        let duration = event.processes[0]
            .get("duration_ms")
            .and_then(|v| v.as_u64())
            .expect("duration_ms must be present");
        assert!(
            duration < 60_000,
            "normal duration must not wrap to a huge value"
        );
    }

    const TRACE_ID: &str = "0af7651916cd43dd8448eb211c80319c";
    const SPAN_ID: &str = "b7ad6b7169203331";

    #[test]
    fn carrier_round_trips_version_and_not_sampled_flag() {
        let header = format!("01-{}-{}-00", TRACE_ID, SPAN_ID);
        let carrier = ContextCarrier::from_traceparent(&header).expect("valid traceparent");
        assert_eq!(carrier.traceparent().as_deref(), Some(header.as_str()));
    }

    #[test]
    fn locally_built_carrier_keeps_default_version_and_sampled_flag() {
        let carrier = ContextCarrier::new()
            .with_trace_id(TRACE_ID)
            .with_span_id(SPAN_ID);
        let expected = format!("00-{}-{}-01", TRACE_ID, SPAN_ID);
        assert_eq!(carrier.traceparent().as_deref(), Some(expected.as_str()));
    #[test]
    fn context_carrier_rejects_spec_invalid_traceparent() {
        // W3C Trace Context: reserved version `ff` and all-zero IDs are invalid.
        assert!(ContextCarrier::from_traceparent(
            "ff-00000000000000000000000000000000-0000000000000000-00"
        )
        .is_none());
        assert!(ContextCarrier::from_traceparent(
            "00-00000000000000000000000000000000-b7ad6b7169203331-01"
        )
        .is_none());
        assert!(ContextCarrier::from_traceparent(
            "00-0af7651916cd43dd8448eb211c80319c-0000000000000000-01"
        )
        .is_none());
    }
}
