//! Regression coverage for the C API exported by `cortex-match`.
//!
//! `ffi.rs` was orphaned (never declared in `lib.rs`), so none of the
//! `#[no_mangle] extern "C"` symbols existed in the built library. This
//! integration test compiles only when the module is wired into the crate and
//! exercises each exported entry point through its public path.

use std::ffi::{CStr, CString};
use std::os::raw::c_char;

use cortex_match::ffi::{free_c_string, score_ffi, shape_hash_ffi, topk_search_ffi};
use cortex_match::{compute_shape_hash, Signature, SymptomClass};
use serde_json::json;

fn sample(shape: &str, feature: f64) -> Signature {
    Signature {
        shape: shape.to_string(),
        symptom_classes: vec![SymptomClass::Latency],
        temporal_sequence: Vec::new(),
        resolution_pattern: Vec::new(),
        feature_vector: vec![feature],
    }
}

fn json_c(value: &str) -> CString {
    CString::new(value).expect("fixture JSON must not contain interior NUL bytes")
}

/// Read a C string returned by the library and release it with the library's
/// own deallocator.
fn take(ptr: *mut c_char) -> String {
    assert!(!ptr.is_null(), "ffi entry point returned a null string");
    let value = unsafe { CStr::from_ptr(ptr) }
        .to_str()
        .expect("ffi entry point returned invalid UTF-8")
        .to_owned();
    free_c_string(ptr);
    value
}

#[test]
fn score_ffi_scores_serialized_signatures() {
    let query = json_c(&serde_json::to_string(&sample("Latency", 1.0)).unwrap());

    let score = score_ffi(query.as_ptr(), query.as_ptr());
    assert!(
        (score - 1.0).abs() < 1e-9,
        "identical signatures should score 1.0, got {score}"
    );

    // Null/invalid inputs degrade to 0.0 rather than trapping across the ABI.
    assert_eq!(score_ffi(std::ptr::null(), query.as_ptr()), 0.0);
    let invalid = json_c("not json");
    assert_eq!(score_ffi(query.as_ptr(), invalid.as_ptr()), 0.0);
}

#[test]
fn topk_search_ffi_returns_signature_id_not_missing_index() {
    let query = json_c(&serde_json::to_string(&sample("Latency", 1.0)).unwrap());
    let candidates =
        json_c(&serde_json::to_string(&[sample("Latency", 1.0), sample("Error", 0.0)]).unwrap());

    let raw = take(topk_search_ffi(query.as_ptr(), candidates.as_ptr(), 1));
    let results: Vec<serde_json::Value> = serde_json::from_str(&raw).expect("valid JSON result");

    assert_eq!(results.len(), 1);
    assert_eq!(results[0]["signature_id"], json!("Latency"));
    assert!(
        (results[0]["similarity"].as_f64().unwrap() - 1.0).abs() < 1e-9,
        "best match should be the identical signature"
    );
    // `ScoredMatch` has no `index` field; the old FFI code referenced it and
    // would not compile once the module was linked in.
    assert!(results[0].get("index").is_none());

    // Null input yields an empty result that is still owned by the caller.
    assert_eq!(
        take(topk_search_ffi(std::ptr::null(), candidates.as_ptr(), 1)),
        "[]"
    );

    // free_c_string accepts null.
    free_c_string(std::ptr::null_mut());
}

#[test]
fn shape_hash_ffi_returns_the_shape_hash() {
    let expected = sample("Latency", 1.0);
    let serialized = json_c(&serde_json::to_string(&expected).unwrap());

    assert_eq!(
        take(shape_hash_ffi(serialized.as_ptr())),
        compute_shape_hash(&expected)
    );
    assert_eq!(take(shape_hash_ffi(std::ptr::null())), "");
}
