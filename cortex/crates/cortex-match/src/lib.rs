//! cortex-match: High-performance incident shape matching for LOZA Cortex
//!
//! CPU-heavy pure algorithm work:
//! - incident shape similarity scoring
//! - top-k similar incident search  
//! - graph pattern matching
//! - topology-independent matching
//! - feature vector distance
//! - fast pattern comparison
//! - approximate matching

pub mod ffi;
pub mod graph;
pub mod signature;
pub mod similarity;
pub mod topk;

pub use graph::{Edge, EdgeType, IncidentGraph, Node, NodeType};
pub use signature::{compute_shape_hash, normalize_signature, Event, Signature, SymptomClass};
pub use similarity::ScoredMatch;
pub use topk::TopK;

/// Main matcher interface - matches incidents by shape
pub struct Matcher {
    topk: TopK,
}

impl Matcher {
    pub fn new() -> Self {
        Self { topk: TopK::new(5) }
    }

    pub fn with_k(mut self, k: usize) -> Self {
        self.topk = self.topk.with_k(k);
        self
    }

    pub fn with_threshold(mut self, threshold: f64) -> Self {
        self.topk = self.topk.with_threshold(threshold);
        self
    }

    pub fn with_metric(mut self, metric: &str) -> Self {
        self.topk = self.topk.with_metric(metric);
        self
    }

    /// Score a query signature against a candidate
    pub fn score(&self, query: &Signature, candidate: &Signature) -> f64 {
        similarity::score(query, candidate, self.topk.metric())
    }

    /// Find top-k similar signatures to query
    pub fn top_k(&self, query: &Signature, candidates: &[Signature]) -> Vec<ScoredMatch> {
        self.topk.search(query, candidates)
    }

    /// Find top-k using a specific metric
    pub fn search(
        &self,
        query: &Signature,
        candidates: &[Signature],
        metric: &str,
    ) -> Vec<ScoredMatch> {
        // Carry over the full configured state (notably the threshold) while
        // allowing the caller to override the metric for this search.
        let tk = self.topk.clone().with_metric(metric);
        tk.search(query, candidates)
    }
}

impl Default for Matcher {
    fn default() -> Self {
        Self::new()
    }
}

/// Convenient function for matching a query incident against candidates
/// Returns top-k closest matches with similarity scores
///
/// # Example
///
/// ```ignore
/// let query = normalize_signature(&events, &services);
/// let candidates = load_signatures(); // from DB
/// let matches = match_incident(&query, &candidates, 5);
/// for m in matches {
///     println!("{}: {:.2}", m.signature_id, m.similarity);
/// }
/// ```
pub fn match_incident(query: &Signature, candidates: &[Signature], k: usize) -> Vec<ScoredMatch> {
    let topk = TopK::new(k).with_metric("shape");
    topk.search(query, candidates)
}

/// Match using topology-independent similarity (ignores service names)
pub fn match_topology_independent(
    query: &Signature,
    candidates: &[Signature],
    k: usize,
) -> Vec<ScoredMatch> {
    let topk = TopK::new(k).with_metric("topology");
    topk.search(query, candidates)
}

/// Fast pattern comparison for graph matching
pub fn match_graph(
    query: &graph::IncidentGraph,
    candidates: &[graph::IncidentGraph],
    k: usize,
) -> Vec<ScoredMatch> {
    graph::match_graph_pattern(query, candidates, 0.5)
        .into_iter()
        .take(k)
        .collect()
}

/// Version info
pub const VERSION: &str = env!("CARGO_PKG_VERSION");

#[cfg(test)]
mod tests {
    use super::*;
    use signature::{Signature, SymptomClass};
    use std::cmp::Ordering;

    #[test]
    fn test_matcher_interface() {
        let matcher = Matcher::new().with_k(10).with_threshold(0.3);

        let query = Signature::from_incident(
            &["deploy".to_string()],
            &["api".to_string()],
            &["latency_spike".to_string()],
            &["rollback".to_string()],
        );

        let candidates: Vec<Signature> = (0..50)
            .map(|_| {
                Signature::from_incident(
                    &["deploy".to_string()],
                    &["svc".to_string()],
                    &["latency_spike".to_string()],
                    &["rollback".to_string()],
                )
            })
            .collect();

        let matches = matcher.top_k(&query, &candidates);
        assert_eq!(matches.len(), 10);
    }

    #[test]
    fn with_k_preserves_configured_threshold_and_metric() {
        let signature = |name: &str, feature_vector: Vec<f64>| {
            let mut signature = Signature::new();
            signature.shape = name.to_string();
            signature.symptom_classes = vec![SymptomClass::Latency];
            signature.feature_vector = feature_vector;
            signature
        };
        let query = signature("query", vec![1.0, 0.0]);
        let weak = signature("weak", vec![0.0, 1.0]);
        let exact = signature("exact", vec![1.0, 0.0]);
        let candidates = vec![weak.clone(), exact];

        let threshold_before_k = Matcher::new()
            .with_threshold(0.8)
            .with_k(2)
            .top_k(&query, &candidates);
        let threshold_after_k = Matcher::new()
            .with_k(2)
            .with_threshold(0.8)
            .top_k(&query, &candidates);

        assert_eq!(threshold_before_k.len(), 1);
        assert_eq!(threshold_after_k.len(), 1);
        assert_eq!(threshold_before_k[0].signature_id, "exact");
        assert_eq!(threshold_after_k[0].signature_id, "exact");

        let cosine_before_k = Matcher::new()
            .with_metric("cosine")
            .with_k(2)
            .score(&query, &weak);
        let cosine_after_k = Matcher::new()
            .with_k(2)
            .with_metric("cosine")
            .score(&query, &weak);

        assert!(cosine_before_k.abs() < 1e-6);
        assert!((cosine_after_k - cosine_before_k).abs() < 1e-6);
    }

    #[test]
    fn test_score_respects_with_metric() {
        // Identical symptoms, temporal order and feature vectors, but different
        // remediation. "shape" weights the feature vectors and ignores
        // remediation (1.0), while "topology" ignores the feature vectors and
        // penalises the differing resolution pattern (0.8). This makes the two
        // metrics observably distinct for the same pair of signatures.
        let query = Signature {
            shape: "Latency→Timeout".to_string(),
            symptom_classes: vec![SymptomClass::Latency, SymptomClass::Timeout],
            temporal_sequence: vec![0, 5000, 10000],
            resolution_pattern: vec!["restart".to_string()],
            feature_vector: vec![1.0, 0.0, 0.0],
        };
        let candidate = Signature {
            shape: "Latency→Timeout".to_string(),
            symptom_classes: vec![SymptomClass::Latency, SymptomClass::Timeout],
            temporal_sequence: vec![0, 5000, 10000],
            resolution_pattern: vec!["rollback".to_string()],
            feature_vector: vec![1.0, 0.0, 0.0],
        };

        let shape_score = Matcher::new().with_metric("shape").score(&query, &candidate);
        let topology_score = Matcher::new()
            .with_metric("topology")
            .score(&query, &candidate);

        assert!((shape_score - 1.0).abs() < 1e-6);
        assert!((topology_score - 0.8).abs() < 1e-6);
        assert!((shape_score - topology_score).abs() > 1e-6);

        // score() must honour the configured metric instead of hardcoding "shape".
        assert!(
            (topology_score - similarity::score(&query, &candidate, "topology")).abs() < 1e-6
        );
    }

    #[test]
    fn test_search_respects_configured_threshold() {
        // A threshold configured on the matcher must also be honoured when
        // searching via `search` with an explicit metric. The metric may be
        // overridden, but the threshold must be carried over.
        let matcher = Matcher::new().with_k(10).with_threshold(0.9);

        let mut query = Signature::new();
        query.feature_vector = vec![1.0, 0.0];

        let mut high = Signature::new();
        high.shape = "high".to_string();
        high.feature_vector = vec![1.0, 0.0]; // cosine = 1.0

        let mut medium = Signature::new();
        medium.shape = "medium".to_string();
        medium.feature_vector = vec![0.5, 0.5]; // cosine ~= 0.707

        let mut low = Signature::new();
        low.shape = "low".to_string();
        low.feature_vector = vec![0.0, 1.0]; // cosine = 0.0

        let candidates = vec![high, medium, low];

        let matches = matcher.search(&query, &candidates, "cosine");

        assert_eq!(matches.len(), 1, "low-similarity matches must be filtered");
        assert_eq!(matches[0].signature_id, "high");
        assert!(matches[0].similarity >= 0.9);
    }

    #[test]
    fn test_match_incident() {
        let query = normalize_signature(
            &[
                signature::Event {
                    kind: "deploy".to_string(),
                    service: None,
                    timestamp_ms: 0,
                },
                signature::Event {
                    kind: "latency_spike".to_string(),
                    service: None,
                    timestamp_ms: 5000,
                },
                signature::Event {
                    kind: "timeout".to_string(),
                    service: None,
                    timestamp_ms: 10000,
                },
            ],
            &["payments-svc".to_string(), "db".to_string()],
        );

        let candidates: Vec<Signature> = (0..100)
            .map(|_| {
                normalize_signature(
                    &[
                        signature::Event {
                            kind: "deploy".to_string(),
                            service: None,
                            timestamp_ms: 0,
                        },
                        signature::Event {
                            kind: "latency_spike".to_string(),
                            service: None,
                            timestamp_ms: 5000,
                        },
                        signature::Event {
                            kind: "timeout".to_string(),
                            service: None,
                            timestamp_ms: 10000,
                        },
                    ],
                    &["billing-svc".to_string(), "postgres".to_string()],
                )
            })
            .collect();

        let matches = match_incident(&query, &candidates, 5);
        assert_eq!(matches.len(), 5);
    }

    #[test]
    fn test_topology_independent() {
        let query = normalize_signature(
            &[
                signature::Event {
                    kind: "deploy".to_string(),
                    service: None,
                    timestamp_ms: 0,
                },
                signature::Event {
                    kind: "latency_spike".to_string(),
                    service: None,
                    timestamp_ms: 5000,
                },
            ],
            &["payments-svc".to_string()],
        );

        let candidates = vec![
            normalize_signature(
                &[
                    signature::Event {
                        kind: "deploy".to_string(),
                        service: None,
                        timestamp_ms: 0,
                    },
                    signature::Event {
                        kind: "latency_spike".to_string(),
                        service: None,
                        timestamp_ms: 5000,
                    },
                ],
                &["billing-svc".to_string()],
            ),
            normalize_signature(
                &[
                    signature::Event {
                        kind: "deploy".to_string(),
                        service: None,
                        timestamp_ms: 0,
                    },
                    signature::Event {
                        kind: "error_rate".to_string(),
                        service: None,
                        timestamp_ms: 5000,
                    },
                ],
                &["invoice-svc".to_string()],
            ),
        ];

        let matches = match_topology_independent(&query, &candidates, 2);
        assert!(matches[0].similarity > matches[1].similarity);
    }

    #[test]
    fn test_version() {
        assert!(!VERSION.is_empty());
    }

    fn strings(vals: &[&str]) -> Vec<String> {
        vals.iter().map(|s| s.to_string()).collect()
    }

    fn varied_candidates() -> Vec<Signature> {
        let symptom_sets: &[&[&str]] = &[
            &["latency_spike"],
            &["error_rate"],
            &["timeout"],
            &["deploy_failed"],
            &["latency_spike", "error_rate"],
            &["timeout", "deploy_failed"],
        ];
        symptom_sets
            .iter()
            .enumerate()
            .map(|(i, symptoms)| {
                let mut sig = Signature::from_incident(
                    &strings(&["deploy"]),
                    &strings(&["api"]),
                    &strings(*symptoms),
                    &strings(&["rollback"]),
                );
                sig.shape = format!("cand-{}", i);
                sig
            })
            .collect()
    }

    /// Assert the caller returned the k highest scores. Comparison is by score
    /// (not id) because a tie at the k boundary may keep either tied candidate.
    fn assert_matches_brute(
        actual: &[ScoredMatch],
        query: &Signature,
        candidates: &[Signature],
        k: usize,
        metric: &str,
    ) {
        let mut all: Vec<f64> = candidates
            .iter()
            .map(|cand| similarity::score(query, cand, metric))
            .collect();
        all.sort_by(|a, b| b.partial_cmp(a).unwrap_or(Ordering::Equal));

        let expected_len = all.len().min(k);
        assert_eq!(actual.len(), expected_len);

        let mut got: Vec<f64> = actual.iter().map(|m| m.similarity).collect();
        got.sort_by(|a, b| b.partial_cmp(a).unwrap_or(Ordering::Equal));
        for (g, e) in got.iter().zip(all.iter().take(expected_len)) {
            assert!((g - e).abs() < 1e-9, "expected {}, got {}", e, g);
        }

        // No excluded candidate may outrank a returned one.
        if expected_len < all.len() {
            let min_returned = *got.last().unwrap();
            let max_excluded = all[expected_len];
            assert!(min_returned + 1e-9 >= max_excluded);
        }
    }

    fn sample_query() -> Signature {
        Signature::from_incident(
            &strings(&["deploy"]),
            &strings(&["api"]),
            &strings(&["latency_spike"]),
            &strings(&["rollback"]),
        )
    }

    #[test]
    fn test_matcher_top_k_returns_highest_scores() {
        let query = sample_query();
        let candidates = varied_candidates();
        let results = Matcher::new().with_k(3).top_k(&query, &candidates);
        assert_matches_brute(&results, &query, &candidates, 3, "shape");
    }

    #[test]
    fn test_match_incident_returns_highest_scores() {
        let query = sample_query();
        let candidates = varied_candidates();
        let results = match_incident(&query, &candidates, 3);
        assert_matches_brute(&results, &query, &candidates, 3, "shape");
    }

    #[test]
    fn test_match_topology_independent_returns_highest_scores() {
        let query = sample_query();
        let candidates = varied_candidates();
        let results = match_topology_independent(&query, &candidates, 3);
        assert_matches_brute(&results, &query, &candidates, 3, "topology");
    }
}
