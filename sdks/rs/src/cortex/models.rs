//! Legacy `cortex::models` path, kept for backward compatibility.
//!
//! The canonical definitions of these types live in [`super::schema`].
//! They are re-exported here so the old `models` path keeps working while
//! there is exactly one source of truth that cannot silently drift.

pub use super::schema::{GraphView, IncidentContext, Remediation, RemediationFeedback};
