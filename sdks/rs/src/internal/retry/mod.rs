use std::time::Duration;

#[derive(Clone, Debug)]
pub struct RetryPolicy {
    pub max_attempts: u32,
    pub base_delay: Duration,
    pub max_delay: Duration,
}

impl Default for RetryPolicy {
    fn default() -> Self {
        Self {
            max_attempts: 3,
            base_delay: Duration::from_millis(100),
            max_delay: Duration::from_secs(5),
        }
    }
}

impl RetryPolicy {
    pub fn delay(&self, attempt: u32) -> Duration {
        if attempt == 0 {
            return Duration::ZERO;
        }
        // Casting `attempt` to i32 wrapped once it exceeded i32::MAX, which
        // underflowed the exponent to ~0 (a near-zero delay), and `as i32 - 1`
        // overflowed outright at attempt == 2^31. Keep the exponent in
        // u32/f64 space instead: a large exponent saturates the f64 value,
        // and the saturating float-to-u64 cast is then clamped to max_delay.
        let ms = self.base_delay.as_millis() as f64 * 2.0_f64.powf((attempt - 1) as f64);
        let backoff = Duration::from_millis(ms as u64);
        backoff.min(self.max_delay)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn large_attempt_counts_saturate_to_max_delay() {
        let policy = RetryPolicy::default();

        // These values used to wrap when cast to i32. At 2^31 the old
        // `attempt as i32 - 1` overflowed (debug panic / release wrap), and
        // just above it the negative exponent underflowed to ~0, returning a
        // near-zero delay instead of max_delay.
        for attempt in [
            i32::MAX as u32 + 1, // 2^31
            i32::MAX as u32 + 2, // first value whose i32 cast wraps negative
            u32::MAX,
        ] {
            assert_eq!(
                policy.delay(attempt),
                policy.max_delay,
                "unexpected delay for attempt {attempt}"
            );
        }
    }

    #[test]
    fn small_attempt_counts_are_unchanged() {
        let policy = RetryPolicy::default();
        assert_eq!(policy.delay(0), Duration::ZERO);
        assert_eq!(policy.delay(1), Duration::from_millis(100));
        assert_eq!(policy.delay(20), policy.max_delay);
    }
}
