use super::super::clock::unix_millis;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Mutex, OnceLock};

type IdGenerator = Box<dyn Fn() -> String + Send + Sync>;

static CUSTOM_ID_GEN: OnceLock<Mutex<Option<IdGenerator>>> = OnceLock::new();
static ID_COUNTER: AtomicU64 = AtomicU64::new(0);

/// Build a unique, prefixed identifier.
///
/// Deriving an ID from the millisecond clock alone makes every context
/// created within the same millisecond share the same ID (and the test clock
/// can be frozen, making that collision deterministic). Mixing in a
/// process-wide monotonic counter and a random suffix guarantees uniqueness
/// across same-millisecond and frozen-clock cases while keeping the
/// recognizable `<prefix>_<millis>_...` shape.
fn generate_id(prefix: &str) -> String {
    let seq = ID_COUNTER.fetch_add(1, Ordering::Relaxed);
    let random = fastrand::u64(..);
    format!("{}_{}_{:x}_{:x}", prefix, unix_millis(), seq, random)
}

pub fn new_event_id() -> String {
    if let Some(gen) = CUSTOM_ID_GEN
        .get_or_init(|| Mutex::new(None))
        .lock()
        .unwrap()
        .as_ref()
    {
        return gen();
    }
    generate_id("evt")
}

/// Generate a unique request identifier. It has the same uniqueness
/// guarantees as [`new_event_id`] but is not affected by the event-ID test
/// generator override.
pub fn new_request_id() -> String {
    generate_id("req")
}

pub fn set_id_generator(f: IdGenerator) {
    *CUSTOM_ID_GEN
        .get_or_init(|| Mutex::new(None))
        .lock()
        .unwrap() = Some(f);
}

pub fn reset_id_generator() {
    *CUSTOM_ID_GEN
        .get_or_init(|| Mutex::new(None))
        .lock()
        .unwrap() = None;
}
