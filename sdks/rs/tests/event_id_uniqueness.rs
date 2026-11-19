use loza::{EventContext, Params};
use std::collections::HashSet;

/// Regression test for #884: identifiers derived solely from the millisecond
/// clock collide for contexts created within the same millisecond. Freezing
/// the clock makes that scenario deterministic.
#[test]
fn generated_ids_are_unique_within_the_same_millisecond() {
    loza::testkit::reset_for_test();
    loza::testkit::fake_clock(1_700_000_000_000);

    const N: usize = 256;
    let mut event_ids = HashSet::new();
    let mut request_ids = HashSet::new();
    for _ in 0..N {
        let ctx = EventContext::new("svc", Params::new("evt"));
        assert!(
            event_ids.insert(ctx.event_id.clone()),
            "event_id collided for two contexts created in the same millisecond"
        );
        assert!(
            request_ids.insert(ctx.request_id.clone()),
            "request_id collided for two contexts created in the same millisecond"
        );
    }
    assert_eq!(event_ids.len(), N);
    assert_eq!(request_ids.len(), N);

    loza::testkit::reset_for_test();
}
