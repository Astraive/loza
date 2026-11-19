use super::super::clock::unix_millis;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};

type IdGenerator = Box<dyn Fn() -> String + Send + Sync>;
// Stored behind an `Arc` so the generator can be cloned out of the critical
// section and invoked after the mutex guard has been dropped. `Box<dyn Fn>`
// is not `Clone`, so cloning the box itself is not possible.
type SharedIdGenerator = Arc<dyn Fn() -> String + Send + Sync>;

static CUSTOM_ID_GEN: OnceLock<Mutex<Option<SharedIdGenerator>>> = OnceLock::new();
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

/// Lock the generator slot, recovering the guard if a previous holder panicked.
///
/// The stored value is just an `Option<Arc<dyn Fn>>` with no invariant to
/// protect, so a poisoned guard can safely be recovered instead of letting one
/// panic cascade into every later ID lookup.
fn lock_generator() -> std::sync::MutexGuard<'static, Option<SharedIdGenerator>> {
    CUSTOM_ID_GEN
        .get_or_init(|| Mutex::new(None))
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

pub fn new_event_id() -> String {
    let gen = {
        let guard = lock_generator();
        (*guard).clone()
    };
    // The lock is released before the caller-provided closure runs. Invoking
    // it under the lock deadlocked any generator that re-entered this module.
    if let Some(gen) = gen {
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
    *lock_generator() = Some(SharedIdGenerator::from(f));
}

pub fn reset_id_generator() {
    *lock_generator() = None;
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::panic::{catch_unwind, AssertUnwindSafe};

    #[test]
    fn panicking_generator_does_not_permanently_poison_id_generator() {
        // A panicking generator is now invoked after the guard is dropped, so
        // it no longer poisons the mutex; the recovery path is still exercised
        // defensively by `lock_generator()` and remains covered here.
        set_id_generator(Box::new(|| panic!("generator exploded")));
        assert!(catch_unwind(AssertUnwindSafe(new_event_id)).is_err());

        // A panicking generator must not cascade into later calls.
        set_id_generator(Box::new(|| "evt_recovered".to_string()));
        assert_eq!(new_event_id(), "evt_recovered");

        // A second panic must be recoverable too, and reset remains usable.
        set_id_generator(Box::new(|| panic!("generator exploded again")));
        assert!(catch_unwind(AssertUnwindSafe(new_event_id)).is_err());
        reset_id_generator();
        assert!(new_event_id().starts_with("evt_"));

        // Leave the shared generator slot clean for other tests.
        reset_id_generator();
    }
}
