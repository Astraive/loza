use std::sync::{Arc, Mutex, MutexGuard, OnceLock};
use std::time::{SystemTime, UNIX_EPOCH};

static FROZEN_TIME_MS: OnceLock<Mutex<Option<u128>>> = OnceLock::new();

fn lock_or_recover<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    match mutex.lock() {
        Ok(guard) => guard,
        Err(poisoned) => poisoned.into_inner(),
    }
}

pub fn unix_millis() -> u128 {
    let guard = lock_or_recover(FROZEN_TIME_MS.get_or_init(|| Mutex::new(None)));
    if let Some(frozen) = guard.as_ref() {
        return *frozen;
    }
    millis_since_epoch(SystemTime::now())
}

/// Convert an instant to milliseconds relative to the Unix epoch.
///
/// `duration_since` fails when `now` is before the epoch. Silently substituting
/// `Duration::default()` made every pre-epoch clock report exactly `0` ms, so
/// downstream consumers generated colliding `evt_0` IDs and `0.json` queue
/// filenames. Instead, use the magnitude of the offset so distinct instants
/// still map to distinct, non-zero values.
fn millis_since_epoch(now: SystemTime) -> u128 {
    now.duration_since(UNIX_EPOCH)
        .map(|elapsed| elapsed.as_millis())
        .unwrap_or_else(|error| error.duration().as_millis())
    drop(guard);
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis()
}

pub fn freeze_at(ms: u128) {
    *lock_or_recover(FROZEN_TIME_MS.get_or_init(|| Mutex::new(None))) = Some(ms);
}

pub fn unfreeze() {
    *lock_or_recover(FROZEN_TIME_MS.get_or_init(|| Mutex::new(None))) = None;
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lock_or_recover_recovers_from_poisoned_mutex() {
        let mutex = Arc::new(Mutex::new(Some(99u128)));
        let worker_mutex = Arc::clone(&mutex);
        let _ = std::thread::spawn(move || {
            let _guard = worker_mutex.lock().unwrap();
            panic!("poison mutex for regression test");
        })
        .join();
        assert!(mutex.is_poisoned());

        assert_eq!(*lock_or_recover(&mutex), Some(99));
        *lock_or_recover(&mutex) = None;
        assert_eq!(*lock_or_recover(&mutex), None);
    }

    #[test]
    fn clock_accessors_survive_poisoned_global_mutex() {
        let _ = std::thread::spawn(|| {
            let _guard = FROZEN_TIME_MS
                .get_or_init(|| Mutex::new(None))
                .lock()
                .unwrap();
            panic!("poison process-wide clock mutex for regression test");
        })
        .join();
        assert!(FROZEN_TIME_MS.get().unwrap().is_poisoned());

        // Before the fix every accessor below panicked on the poisoned mutex,
        // disabling the process-wide clock used by ID/filename generation.
        assert!(unix_millis() > 0);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    #[test]
    fn post_epoch_millis_are_the_epoch_offset() {
        let now = UNIX_EPOCH + Duration::from_millis(1_234);
        assert_eq!(millis_since_epoch(now), 1_234);
    }

    #[test]
    fn pre_epoch_millis_do_not_collapse_to_zero() {
        let now = UNIX_EPOCH - Duration::from_millis(1_500);
        let millis = millis_since_epoch(now);
        assert_ne!(millis, 0, "pre-epoch clock must not collapse to 0 ms");
        assert_eq!(millis, 1_500);
    }

    #[test]
    fn distinct_pre_epoch_instants_stay_distinct() {
        let earlier = UNIX_EPOCH - Duration::from_millis(2_500);
        let later = UNIX_EPOCH - Duration::from_millis(1_500);
        assert_ne!(millis_since_epoch(earlier), millis_since_epoch(later));
    }
}
