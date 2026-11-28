use std::cmp::Ordering;
use std::collections::BinaryHeap;

use crate::signature::Signature;
use crate::similarity::{self, ScoredMatch};

/// Priority queue item for top-k search
#[derive(Debug, Clone)]
struct PQItem {
    score: f64,
    signature_id: String,
}

impl PartialEq for PQItem {
    fn eq(&self, other: &Self) -> bool {
        self.score == other.score
    }
}

impl Eq for PQItem {}

impl PartialOrd for PQItem {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

impl Ord for PQItem {
    fn cmp(&self, other: &Self) -> Ordering {
        // Invert the comparison so BinaryHeap behaves as a min-heap: peek()
        // then returns the smallest score currently retained, which is the
        // candidate that should be evicted when a better one arrives.
        other
            .score
            .partial_cmp(&self.score)
            .unwrap_or(Ordering::Equal)
    }
}

/// Retain the `k` highest `(score, signature_id)` pairs using a min-heap.
///
/// [`PQItem`]'s reversed [`Ord`] implementation makes `BinaryHeap` behave as a
/// min-heap, so `peek()` returns the *smallest* stored score. A new candidate
/// evicts that smallest element only when it scores higher, leaving the k
/// highest scores. (Do not wrap items in `Reverse` here: that would invert the
/// comparison a second time and turn this back into a max-heap that keeps the
/// lowest scores.)
fn retain_top_k<I>(scored: I, k: usize, threshold: f64) -> Vec<ScoredMatch>
where
    I: IntoIterator<Item = (f64, String)>,
{
    if k == 0 {
        return Vec::new();
    }

    let mut heap: BinaryHeap<PQItem> = BinaryHeap::with_capacity(k);

    for (score, signature_id) in scored {
        if score < threshold {
            continue;
        }

        let item = PQItem {
            score,
            signature_id,
        };

        if heap.len() < k {
            heap.push(item);
        } else if score > heap.peek().map(|p| p.score).unwrap_or(f64::NEG_INFINITY) {
            heap.pop();
            heap.push(item);
        }
    }

    let mut results: Vec<ScoredMatch> = heap
        .into_iter()
        .map(|item| ScoredMatch::new(item.signature_id, item.score))
        .collect();

    results.sort_by(|a, b| {
        b.similarity
            .partial_cmp(&a.similarity)
            .unwrap_or(Ordering::Equal)
    });
    results
}

/// Efficient top-k matching using priority queue
/// Avoids sorting all candidates, only maintains heap of size k
#[derive(Clone)]
pub struct TopK {
    k: usize,
    threshold: f64,
    metric: String,
}

impl TopK {
    pub fn new(k: usize) -> Self {
        Self {
            k,
            threshold: 0.0,
            metric: "shape".to_string(),
        }
    }

    pub(crate) fn with_k(mut self, k: usize) -> Self {
        self.k = k;
        self
    }

    pub fn with_threshold(mut self, threshold: f64) -> Self {
        self.threshold = threshold;
        self
    }

    pub fn with_metric(mut self, metric: &str) -> Self {
        self.metric = metric.to_string();
        self
    }

    pub fn k(&self) -> usize {
        self.k
    }

    pub fn metric(&self) -> &str {
        &self.metric
    }

    /// Search for top-k similar signatures
    /// O(n * d) where n = candidates, d = feature vector dim
    /// Using heap: O(n log k) instead of O(n log n)
    pub fn search(&self, query: &Signature, candidates: &[Signature]) -> Vec<ScoredMatch> {
        let k = self.k.min(candidates.len());
        if k == 0 {
            return Vec::new();
        }

        let scored = candidates.iter().map(|cand| {
            (
                similarity::score(query, cand, &self.metric),
                cand.shape.clone(),
            )
        });

        retain_top_k(scored, k, self.threshold)
    }

    /// Batch search for multiple queries
    pub fn search_batch(
        &self,
        queries: &[Signature],
        candidates: &[Signature],
    ) -> Vec<Vec<ScoredMatch>> {
        queries.iter().map(|q| self.search(q, candidates)).collect()
    }
}

/// Brute force for comparison/verification
pub fn search_brute(
    query: &Signature,
    candidates: &[Signature],
    k: usize,
    metric: &str,
) -> Vec<ScoredMatch> {
    let mut results: Vec<ScoredMatch> = candidates
        .iter()
        .map(|cand| {
            let score = similarity::score(query, cand, metric);
            ScoredMatch {
                signature_id: cand.shape.clone(),
                similarity: score,
                matched_symptoms: 0,
                temporal_similarity: 0.0,
            }
        })
        .collect();

    results.sort_by(|a, b| {
        b.similarity
            .partial_cmp(&a.similarity)
            .unwrap_or(Ordering::Equal)
    });
    results.truncate(k);
    results
}

/// Approximate nearest neighbor using bucketing
/// Divides feature space into buckets for faster search
pub struct ANNIndex {
    signatures: Vec<Signature>,
    _bucket_size: usize,
}

impl ANNIndex {
    pub fn new(bucket_size: usize) -> Self {
        Self {
            signatures: Vec::new(),
            _bucket_size: bucket_size,
        }
    }

    pub fn add(&mut self, sig: Signature) {
        self.signatures.push(sig);
    }

    pub fn build(candidates: &[Signature], bucket_size: usize) -> Self {
        let mut index = Self::new(bucket_size);
        index.signatures = candidates.to_vec();
        index
    }

    pub fn search(&self, query: &Signature, k: usize, metric: &str) -> Vec<ScoredMatch> {
        // Simple bucketing: search all, let TopK handle it
        let topk = TopK::new(k).with_metric(metric);
        topk.search(query, &self.signatures)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::signature::Signature;

    #[test]
    fn test_topk() {
        let query = Signature::from_incident(
            &["deploy".to_string()],
            &["api".to_string()],
            &["latency_spike".to_string()],
            &["rollback".to_string()],
        );

        let candidates: Vec<Signature> = (0..20)
            .map(|i| {
                if i % 3 == 0 {
                    Signature::from_incident(
                        &["deploy".to_string()],
                        &["api".to_string()],
                        &["latency_spike".to_string()],
                        &["rollback".to_string()],
                    )
                } else {
                    Signature::from_incident(
                        &["deploy".to_string()],
                        &["db".to_string()],
                        &["error_rate".to_string()],
                        &["restart".to_string()],
                    )
                }
            })
            .collect();

        let topk = TopK::new(5).with_metric("shape");
        let results = topk.search(&query, &candidates);

        assert_eq!(results.len(), 5);
        assert!(results[0].similarity >= results[1].similarity);
    }

    /// Regression test for the max-heap top-k defect: a mid-range score that
    /// arrives after the heap is full must still displace the weakest kept
    /// candidate. With a max-heap, peek() was the *largest* score, so the
    /// replacement branch was taken only when a score beat the current best,
    /// causing scores that belong in the top-k to be discarded.
    #[test]
    fn test_topk_replaces_weakest_not_best() {
        let query = Signature {
            feature_vector: vec![1.0, 0.0],
            ..Signature::new()
        };

        let candidate = |x: f64, y: f64, shape: &str| {
            let mut sig = Signature::new();
            sig.feature_vector = vec![x, y];
            sig.shape = shape.to_string();
            sig
        };

        // cosine similarity against [1.0, 0.0]; each vector is unit length.
        let candidates = vec![
            candidate(1.0, 0.0, "best"),          // score 1.0
            candidate(0.5, 0.8660254, "weak"),    // score 0.5
            candidate(0.9, 0.43588989, "mid"),    // score 0.9 <- must displace "weak"
        ];

        let topk = TopK::new(2).with_metric("cosine");
        let results = topk.search(&query, &candidates);
        let brute = search_brute(&query, &candidates, 2, "cosine");

        assert_eq!(results.len(), 2);
        for (got, expected) in results.iter().zip(brute.iter()) {
            assert!(
                (got.similarity - expected.similarity).abs() < 1e-6,
                "top-k {:?} differs from brute-force top-k {:?}",
                got,
                expected
            );
        }
        assert!((results[0].similarity - 1.0).abs() < 1e-6);
        assert!((results[1].similarity - 0.9).abs() < 1e-6);
        assert_eq!(results[1].signature_id, "mid");
    }

    #[test]
    fn test_search_consistency() {
        let query = Signature::from_incident(
            &["deploy".to_string()],
            &["api".to_string()],
            &["latency_spike".to_string()],
            &[],
        );

        let candidates: Vec<Signature> = (0..100)
            .map(|_| {
                Signature::from_incident(
                    &["deploy".to_string()],
                    &["svc".to_string()],
                    &["latency_spike".to_string()],
                    &[],
                )
            })
            .collect();

        let topk = TopK::new(10).with_metric("shape");
        let results = topk.search(&query, &candidates);

        let brute = search_brute(&query, &candidates, 10, "shape");

        assert_eq!(results.len(), brute.len());
    }

    #[test]
    fn test_threshold() {
        let query = Signature::new();

        let candidates: Vec<Signature> = (0..10)
            .map(|i| {
                let mut sig = Signature::new();
                sig.feature_vector = vec![i as f64 / 10.0];
                sig
            })
            .collect();

        let topk = TopK::new(5).with_threshold(0.5);
        let results = topk.search(&query, &candidates);

        for r in &results {
            assert!(r.similarity >= 0.5);
        }
    }

    fn match_ids(matches: &[ScoredMatch]) -> Vec<&str> {
        matches.iter().map(|m| m.signature_id.as_str()).collect()
    }

    /// Candidate whose cosine similarity against `[1.0, 0.0]` is exactly `score`.
    fn cosine_candidate(score: f64, id: &str) -> Signature {
        let mut sig = Signature::new();
        sig.shape = id.to_string();
        sig.feature_vector = vec![score, (1.0 - score * score).sqrt()];
        sig
    }

    #[test]
    fn test_topk_retains_highest_scores() {
        // Worked example from the bug report: 5, 3, 8, 1 with k=2 must
        // retain 8 and 5 (the old max-heap kept 8 and 3).
        let scored = vec![
            (5.0, "5".to_string()),
            (3.0, "3".to_string()),
            (8.0, "8".to_string()),
            (1.0, "1".to_string()),
        ];

        let results = retain_top_k(scored, 2, 0.0);

        assert_eq!(match_ids(&results), vec!["8", "5"]);
        assert_eq!(results[0].similarity, 8.0);
        assert_eq!(results[1].similarity, 5.0);
    }

    #[test]
    fn test_topk_search_uses_highest_scores() {
        // Same worked example exercised through the full scoring path.
        let mut query = Signature::new();
        query.feature_vector = vec![1.0, 0.0];

        let candidates = vec![
            cosine_candidate(0.5, "5"),
            cosine_candidate(0.3, "3"),
            cosine_candidate(0.8, "8"),
            cosine_candidate(0.1, "1"),
        ];

        let results = TopK::new(2)
            .with_metric("cosine")
            .search(&query, &candidates);

        assert_eq!(match_ids(&results), vec!["8", "5"]);
        assert!((results[0].similarity - 0.8).abs() < 1e-6);
        assert!((results[1].similarity - 0.5).abs() < 1e-6);
    }

    #[test]
    fn test_topk_k_one() {
        let scored = vec![
            (5.0, "a".to_string()),
            (3.0, "b".to_string()),
            (8.0, "c".to_string()),
            (1.0, "d".to_string()),
        ];

        let results = retain_top_k(scored, 1, 0.0);

        assert_eq!(match_ids(&results), vec!["c"]);
        assert_eq!(results[0].similarity, 8.0);
    }

    #[test]
    fn test_topk_k_greater_than_count() {
        let scored = vec![
            (1.0, "a".to_string()),
            (3.0, "b".to_string()),
            (2.0, "c".to_string()),
        ];

        let results = retain_top_k(scored, 10, 0.0);

        assert_eq!(match_ids(&results), vec!["b", "c", "a"]);
    }

    #[test]
    fn test_topk_ties() {
        let scored = vec![
            (7.0, "a".to_string()),
            (7.0, "b".to_string()),
            (7.0, "c".to_string()),
            (1.0, "d".to_string()),
        ];

        let results = retain_top_k(scored, 2, 0.0);

        assert_eq!(results.len(), 2);
        assert!(results
            .iter()
            .all(|m| (m.similarity - 7.0).abs() < f64::EPSILON));
    }

    #[test]
    fn test_topk_negative_scores() {
        let scored = vec![
            (-1.0, "a".to_string()),
            (-5.0, "b".to_string()),
            (-3.0, "c".to_string()),
            (-9.0, "d".to_string()),
        ];

        let results = retain_top_k(scored, 2, f64::NEG_INFINITY);

        assert_eq!(match_ids(&results), vec!["a", "c"]);
        assert_eq!(results[0].similarity, -1.0);
        assert_eq!(results[1].similarity, -3.0);
    }

    #[test]
    fn test_ann_index_search_retains_highest() {
        let mut query = Signature::new();
        query.feature_vector = vec![1.0, 0.0];
        let candidates = vec![
            cosine_candidate(0.2, "a"),
            cosine_candidate(0.9, "b"),
            cosine_candidate(0.4, "c"),
        ];

        let index = ANNIndex::build(&candidates, 10);
        let results = index.search(&query, 2, "cosine");

        assert_eq!(match_ids(&results), vec!["b", "c"]);
    }
}
