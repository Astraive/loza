use super::super::clock::unix_millis;
use std::sync::{Mutex, OnceLock};

type IdGenerator = Box<dyn Fn() -> String + Send + Sync>;

static CUSTOM_ID_GEN: OnceLock<Mutex<Option<IdGenerator>>> = OnceLock::new();

/// Lock the generator slot, recovering the guard if a previous holder panicked.
///
/// A custom generator runs while this lock is held, so a panicking generator
/// poisons the mutex. The stored value is just an `Option<Box<dyn Fn>>` with no
/// invariant to protect, so a poisoned guard can safely be recovered instead of
/// letting one panic cascade into every later ID lookup.
fn lock_generator() -> std::sync::MutexGuard<'static, Option<IdGenerator>> {
    CUSTOM_ID_GEN
        .get_or_init(|| Mutex::new(None))
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

pub fn new_event_id() -> String {
    if let Some(gen) = lock_generator().as_ref() {
        return gen();
    }
    format!("evt_{}", unix_millis())
}

pub fn set_id_generator(f: IdGenerator) {
    *lock_generator() = Some(f);
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
        // A panicking generator runs under the mutex guard, poisoning the lock.
        set_id_generator(Box::new(|| panic!("generator exploded")));
        assert!(catch_unwind(AssertUnwindSafe(new_event_id)).is_err());

        // The poisoned mutex must not cascade into later calls.
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
