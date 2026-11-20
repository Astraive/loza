use super::super::clock::unix_millis;
use std::cell::RefCell;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

type IdGenerator = Box<dyn Fn() -> String + Send + Sync>;
// Stored behind an `Arc` so the generator can be cloned out of the slot and
// invoked after the borrow has been released. `Box<dyn Fn>` is not `Clone`, so
// cloning the box itself is not possible.
type SharedIdGenerator = Arc<dyn Fn() -> String + Send + Sync>;

thread_local! {
    // The generator override is a testkit/testing hook. Keeping it per-thread
    // prevents one test's custom (or panicking) generator from leaking into a
    // different test that happens to run concurrently on another thread.
    static CUSTOM_ID_GEN: RefCell<Option<SharedIdGenerator>> = RefCell::new(None);
}
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
    // Clone the generator out and release the borrow before invoking it: a
    // reentrant generator may call `set_id_generator`/`reset_id_generator`.
    let gen = CUSTOM_ID_GEN.with(|slot| (*slot.borrow()).clone());
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
    CUSTOM_ID_GEN.with(|slot| *slot.borrow_mut() = Some(SharedIdGenerator::from(f)));
}

pub fn reset_id_generator() {
    CUSTOM_ID_GEN.with(|slot| *slot.borrow_mut() = None);
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::internal::clock::{freeze_at, unfreeze};
    use std::collections::HashSet;
    use std::panic::{catch_unwind, AssertUnwindSafe};

    #[test]
    fn panicking_generator_does_not_permanently_poison_id_generator() {
        // A panicking generator is invoked after the slot borrow is released,
        // so it cannot leave the slot in a poisoned/partially-mutated state.
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

    #[test]
    fn default_event_ids_are_unique_within_the_same_millisecond() {
        reset_id_generator();
        freeze_at(1_700_000_000_000);

        let ids: Vec<String> = (0..1_000).map(|_| new_event_id()).collect();

        unfreeze();

        let unique: HashSet<&String> = ids.iter().collect();
        assert_eq!(
            unique.len(),
            ids.len(),
            "default event IDs must not collide"
        );
        assert!(
            ids.iter().all(|id| id.starts_with("evt_")),
            "default event IDs must keep the evt_ prefix"
        );
    }
}
