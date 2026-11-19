use loza::{EventContext, Params};

#[test]
fn event_context_new_honors_custom_id_generator() {
    // A generator registered through the public SetIDGenerator API must be used
    // by production EventContext construction, not only by new_event_id() itself.
    loza::testkit::reset_for_test();
    loza::SetIDGenerator(|| "evt_custom_override".to_string());

    let ctx = EventContext::new("svc", Params::new("custom.id.event"));
    assert_eq!(
        ctx.event_id, "evt_custom_override",
        "EventContext::new must honor a registered custom ID generator"
    );

    let ctx2 = EventContext::new("svc", Params::new("custom.id.event.2"));
    assert_eq!(ctx2.event_id, "evt_custom_override");

    loza::testkit::reset_for_test();
    let ctx3 = EventContext::new("svc", Params::new("default.id.event"));
    assert!(
        ctx3.event_id.starts_with("evt_"),
        "default event id should still start with 'evt_'"
    );
}
