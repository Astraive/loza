use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq, Hash)]
pub enum SymptomClass {
    Latency,
    Error,
    Timeout,
    Resource,
    Deployment,
    Unknown,
}

impl From<&str> for SymptomClass {
    fn from(s: &str) -> Self {
        match s.to_lowercase().as_str() {
            "latency_spike" | "latency" => SymptomClass::Latency,
            "error_rate" | "errors" | "5xx" | "error" => SymptomClass::Error,
            "timeout" | "timed_out" => SymptomClass::Timeout,
            "memory_leak" | "memory" | "cpu_spike" | "cpu" => SymptomClass::Resource,
            "deployment_fail" | "deploy_failed" => SymptomClass::Deployment,
            _ => SymptomClass::Unknown,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Event {
    pub kind: String,
    pub service: Option<String>,
    pub timestamp_ms: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Signature {
    pub shape: String,
    pub symptom_classes: Vec<SymptomClass>,
    pub temporal_sequence: Vec<i64>,
    pub resolution_pattern: Vec<String>,
    pub feature_vector: Vec<f64>,
}

impl Signature {
    pub fn new() -> Self {
        Self {
            shape: String::new(),
            symptom_classes: Vec::new(),
            temporal_sequence: Vec::new(),
            resolution_pattern: Vec::new(),
            feature_vector: Vec::new(),
        }
    }

    pub fn from_causal_chain(events: &[Event]) -> Self {
        let mut sig = Self::new();
        let mut types = Vec::new();
        let mut timestamps = Vec::new();

        for event in events {
            types.push(SymptomClass::from(event.kind.as_str()));
            timestamps.push(event.timestamp_ms);
        }

        sig.symptom_classes = types;

        if !timestamps.is_empty() {
            let first = timestamps[0];
            sig.temporal_sequence = timestamps.iter().map(|&t| t - first).collect();
        }

        let shape_parts: Vec<String> = events
            .iter()
            .map(|e| {
                let class = SymptomClass::from(e.kind.as_str());
                format!("{:?}", class)
            })
            .collect();
        sig.shape = shape_parts.join("→");

        sig.compute_feature_vector();

        sig
    }

    pub fn from_incident(
        causal_chain: &[String],
        services: &[String],
        symptoms: &[String],
        remediation: &[String],
    ) -> Self {
        let mut sig = Self::new();

        sig.symptom_classes = symptoms
            .iter()
            .map(|s| SymptomClass::from(s.as_str()))
            .collect();

        let shape_parts: Vec<String> = sig
            .symptom_classes
            .iter()
            .map(|c| format!("{:?}", c))
            .collect();
        sig.shape = shape_parts.join("→");

        sig.resolution_pattern = remediation.to_vec();

        sig.feature_vector =
            compute_feature_vector(causal_chain.len(), services.len(), &sig.symptom_classes);

        sig
    }

    fn compute_feature_vector(&mut self) {
        let n = self.symptom_classes.len();
        self.feature_vector = compute_feature_vector(n, n, &self.symptom_classes);
    }
}

impl Default for Signature {
    fn default() -> Self {
        Self::new()
    }
}

fn compute_feature_vector(
    n_symptoms: usize,
    n_services: usize,
    symptoms: &[SymptomClass],
) -> Vec<f64> {
    let mut v = Vec::with_capacity(10);
    v.push(n_symptoms as f64);
    v.push(n_services as f64);

    let mut has_latency = 0.0;
    let mut has_error = 0.0;
    let mut has_timeout = 0.0;
    let mut has_resource = 0.0;
    let mut has_deploy = 0.0;

    for s in symptoms {
        match s {
            SymptomClass::Latency => has_latency = 1.0,
            SymptomClass::Error => has_error = 1.0,
            SymptomClass::Timeout => has_timeout = 1.0,
            SymptomClass::Resource => has_resource = 1.0,
            SymptomClass::Deployment => has_deploy = 1.0,
            SymptomClass::Unknown => {}
        }
    }

    v.push(has_latency);
    v.push(has_error);
    v.push(has_timeout);
    v.push(has_resource);
    v.push(has_deploy);

    let unique_symptoms = symptoms
        .iter()
        .collect::<std::collections::HashSet<_>>()
        .len() as f64;
    v.push(unique_symptoms / n_symptoms.max(1) as f64);

    let severity = has_error * 0.3 + has_timeout * 0.3 + has_resource * 0.2 + has_latency * 0.2;
    v.push(severity);
    v.push(0.0);

    v
}

/// Normalize a raw incident to topology-independent signature
/// - ignores exact service names
/// - extracts symptom classes
/// - captures temporal sequence
/// - ignores timing, only order matters
pub fn normalize_signature(causal_chain: &[Event], service_names: &[String]) -> Signature {
    let mut sig = Signature::from_causal_chain(causal_chain);

    let roles: Vec<String> = service_names
        .iter()
        .enumerate()
        .map(|(i, s)| {
            if s.contains("db") || s.contains("postgres") || s.contains("mysql") {
                "database".to_string()
            } else if s.contains("cache") || s.contains("redis") || s.contains("memcached") {
                "cache".to_string()
            } else if s.contains("queue") || s.contains("kafka") || s.contains("rabbit") {
                "queue".to_string()
            } else if s.contains("api") || s.contains("http") {
                "api".to_string()
            } else {
                format!("tier{}", i)
            }
        })
        .collect();

    let role_set: std::collections::HashSet<_> = roles.iter().collect();
    let role_diversity = role_set.len() as f64 / service_names.len().max(1) as f64;
    set_role_diversity_feature(&mut sig, role_diversity);

    sig
}

/// Write the topology role-diversity value into feature slot 9 when present.
///
/// Slot 9 is the last element of a full ten-element feature vector, so a guard
/// based on `len() >= 9` was off by one and panicked on a nine-element vector.
/// Using `get_mut` only writes the slot when it actually exists.
fn set_role_diversity_feature(sig: &mut Signature, role_diversity: f64) {
    if let Some(slot) = sig.feature_vector.get_mut(9) {
        *slot = role_diversity;
    }
}

/// Compute hash of signature shape (topology-independent)
pub fn compute_shape_hash(sig: &Signature) -> String {
    let mut hasher = Sha256::new();
    hasher.update(sig.shape.as_bytes());
    format!("{:x}", hasher.finalize())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_symptom_class() {
        assert_eq!(SymptomClass::from("latency_spike"), SymptomClass::Latency);
        assert_eq!(SymptomClass::from("error_rate"), SymptomClass::Error);
        assert_eq!(SymptomClass::from("timeout"), SymptomClass::Timeout);
    }

    #[test]
    fn test_signature_from_chain() {
        let events = vec![
            Event {
                kind: "deploy".to_string(),
                service: Some("api".to_string()),
                timestamp_ms: 0,
            },
            Event {
                kind: "latency_spike".to_string(),
                service: Some("api".to_string()),
                timestamp_ms: 5000,
            },
            Event {
                kind: "timeout".to_string(),
                service: Some("db".to_string()),
                timestamp_ms: 10000,
            },
            Event {
                kind: "rollback".to_string(),
                service: Some("api".to_string()),
                timestamp_ms: 15000,
            },
        ];

        let sig = Signature::from_causal_chain(&events);

        assert!(sig.shape.contains("Latency"));
        assert!(sig.shape.contains("Timeout"));
        assert!(!sig.temporal_sequence.is_empty());
    }

    #[test]
    fn test_normalize_topology_independent() {
        let events = vec![
            Event {
                kind: "deploy".to_string(),
                service: None,
                timestamp_ms: 0,
            },
            Event {
                kind: "latency_spike".to_string(),
                service: None,
                timestamp_ms: 5000,
            },
            Event {
                kind: "timeout".to_string(),
                service: None,
                timestamp_ms: 10000,
            },
        ];

        let services = vec!["payments-svc".to_string(), "billing-db".to_string()];

        let sig = normalize_signature(&events, &services);

        assert!(!sig.shape.contains("payments"));
        assert!(!sig.shape.contains("billing"));
        assert!(sig.shape.contains("Latency"));
    }

    #[test]
    fn test_normalize_writes_role_diversity_to_last_slot() {
        // Two distinct roles ("api" and "database") across two services -> 1.0.
        let events = vec![Event {
            kind: "deploy".to_string(),
            service: None,
            timestamp_ms: 0,
        }];
        let services = vec!["payments-api".to_string(), "orders-db".to_string()];

        let sig = normalize_signature(&events, &services);

        assert_eq!(sig.feature_vector.len(), 10);
        assert!((sig.feature_vector[9] - 1.0).abs() < f64::EPSILON);
    }

    #[test]
    fn test_role_diversity_short_vector_does_not_panic() {
        // Regression for MASS-0270: a nine-element feature vector previously
        // reached `feature_vector[9]` under a `len() >= 9` guard and panicked
        // with an index-out-of-bounds error. It must be left untouched instead.
        let mut sig = Signature::new();
        sig.feature_vector = vec![0.0; 9];

        set_role_diversity_feature(&mut sig, 0.75);

        assert_eq!(sig.feature_vector.len(), 9);
        assert!(sig.feature_vector.iter().all(|&v| v == 0.0));
    }
}
