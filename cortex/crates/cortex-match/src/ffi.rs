use std::ffi::{CStr, CString};
use std::os::raw::c_char;

use crate::{Matcher, Signature};

fn cstr_to_string(ptr: *const c_char) -> Option<String> {
    if ptr.is_null() {
        return None;
    }
    unsafe { CStr::from_ptr(ptr) }.to_str().ok().map(str::to_owned)
}

/// Score a query signature against a candidate signature.
/// Both arguments are JSON-serialized Signature objects.
/// Returns the similarity score as f64.
#[no_mangle]
pub extern "C" fn score_ffi(
    query_json: *const c_char,
    candidate_json: *const c_char,
) -> f64 {
    let query_str = match cstr_to_string(query_json) {
        Some(s) => s,
        None => return 0.0,
    };
    let candidate_str = match cstr_to_string(candidate_json) {
        Some(s) => s,
        None => return 0.0,
    };

    let query: Signature = match serde_json::from_str(&query_str) {
        Ok(s) => s,
        Err(_) => return 0.0,
    };
    let candidate: Signature = match serde_json::from_str(&candidate_str) {
        Ok(s) => s,
        Err(_) => return 0.0,
    };

    let matcher = Matcher::new();
    matcher.score(&query, &candidate)
}

/// Top-K search: find the k most similar signatures to the query.
/// query_json: JSON-serialized Signature
/// candidates_json: JSON array of Signature objects
/// k: number of results to return
/// Returns: JSON array of {signature_id, similarity} objects
#[no_mangle]
pub extern "C" fn topk_search_ffi(
    query_json: *const c_char,
    candidates_json: *const c_char,
    k: usize,
) -> *mut c_char {
    let query_str = match cstr_to_string(query_json) {
        Some(s) => s,
        None => return CString::new("[]").unwrap().into_raw(),
    };
    let candidates_str = match cstr_to_string(candidates_json) {
        Some(s) => s,
        None => return CString::new("[]").unwrap().into_raw(),
    };

    let query: Signature = match serde_json::from_str(&query_str) {
        Ok(s) => s,
        Err(_) => return CString::new("[]").unwrap().into_raw(),
    };
    let candidates: Vec<Signature> = match serde_json::from_str(&candidates_str) {
        Ok(s) => s,
        Err(_) => return CString::new("[]").unwrap().into_raw(),
    };

    let matcher = Matcher::new().with_k(k);
    let results = matcher.top_k(&query, &candidates);

    let output: Vec<serde_json::Value> = results
        .iter()
        .map(|m| {
            serde_json::json!({
                "signature_id": m.signature_id,
                "similarity": m.similarity,
            })
        })
        .collect();

    let json = serde_json::to_string(&output).unwrap_or_else(|_| "[]".to_string());
    CString::new(json).unwrap().into_raw()
}

/// Compute a topology-independent shape hash for a signature.
/// signature_json: JSON-serialized Signature
/// Returns: JSON string with the hash
#[no_mangle]
pub extern "C" fn shape_hash_ffi(
    signature_json: *const c_char,
) -> *mut c_char {
    let sig_str = match cstr_to_string(signature_json) {
        Some(s) => s,
        None => return CString::new("").unwrap().into_raw(),
    };

    let sig: Signature = match serde_json::from_str(&sig_str) {
        Ok(s) => s,
        Err(_) => return CString::new("").unwrap().into_raw(),
    };

    let hash = crate::compute_shape_hash(&sig);
    CString::new(hash).unwrap().into_raw()
}

/// Free a C string allocated by this library.
#[no_mangle]
pub extern "C" fn free_c_string(ptr: *mut c_char) {
    if !ptr.is_null() {
        unsafe {
            drop(CString::from_raw(ptr));
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::Signature;

    fn signature(shape: &str) -> Signature {
        let mut sig = Signature::new();
        sig.shape = shape.to_string();
        sig
    }

    /// Regression test for the FFI top-k result shape: each entry must expose
    /// the matched candidate's `signature_id` (ScoredMatch has no `index` field).
    #[test]
    fn topk_search_ffi_returns_signature_id_not_index() {
        let query = signature("query-shape");
        let candidates = vec![signature("candidate-alpha"), signature("candidate-beta")];

        let query_json = CString::new(serde_json::to_string(&query).unwrap()).unwrap();
        let candidates_json = CString::new(serde_json::to_string(&candidates).unwrap()).unwrap();

        let raw = topk_search_ffi(query_json.as_ptr(), candidates_json.as_ptr(), 2);
        assert!(!raw.is_null(), "topk_search_ffi returned a null pointer");

        let output = unsafe { CStr::from_ptr(raw) }
            .to_str()
            .expect("FFI output must be valid UTF-8")
            .to_string();
        // Balance the allocation made by `into_raw` in the FFI function.
        unsafe { drop(CString::from_raw(raw)) };

        let results: Vec<serde_json::Value> = serde_json::from_str(&output).unwrap();
        assert_eq!(results.len(), 2);

        let shapes: Vec<&str> = candidates.iter().map(|c| c.shape.as_str()).collect();
        for entry in &results {
            assert!(
                entry.get("index").is_none(),
                "result must not contain a stale `index` key: {entry}"
            );

            let id = entry
                .get("signature_id")
                .and_then(|v| v.as_str())
                .unwrap_or_else(|| panic!("result missing `signature_id` key: {entry}"));
            assert!(
                shapes.contains(&id),
                "`signature_id` must be a candidate shape, got {id}"
            );

            assert!(
                entry.get("similarity").and_then(|v| v.as_f64()).is_some(),
                "result missing `similarity` score: {entry}"
            );
        }
    }
}
