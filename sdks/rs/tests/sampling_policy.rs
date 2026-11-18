#[test]
fn sampling_and_policy_helpers_work() {
    let mut ctx = loza::start_event(None, loza::Params::new("test"));
    loza::finish(&mut ctx);
    assert!(loza::should_sample(&ctx, &loza::sample_all()));
    assert!(!loza::should_sample(&ctx, &loza::sample_none()));
    let _ = loza::sample_by_event(|event| event.event == "test");
    let _ = loza::sample_by_outcome(&["success"]);
    let _ = loza::allow_fields(&["safe"]);
    let _ = loza::block_fields(&["danger"]);
}

#[test]
fn sub_millisecond_rate_limit_window_respects_configured_rate() {
    let mut ctx = loza::start_event(None, loza::Params::new("test"));
    loza::finish(&mut ctx);

    // A 500us window truncates to 0 under `as_millis()`, so the buggy code
    // computed `rate / 0.0 == +inf` and sampled 100% of events, ignoring the
    // configured rate. With fractional milliseconds, 0.25 / 0.5 == 0.5.
    let sampler = loza::SampleRateLimited(0.25, std::time::Duration::from_micros(500));
    const TRIALS: usize = 20_000;
    let sampled = (0..TRIALS)
        .filter(|_| loza::should_sample(&ctx, &sampler))
        .count();

    // ~50% (10,000/20,000) expected; the buggy implementation sampled 100%.
    // Generous bounds keep this robust while still catching the 100% regression.
    assert!(
        sampled > 8_000 && sampled < 12_000,
        "sub-millisecond window sampled {sampled}/{TRIALS} events; configured rate was ignored"
    );
}
