use serde_json::Map;

#[test]
fn lifecycle_event_helpers_work() {
    let mut ctx = loza::start_event(
        None,
        loza::Params::new("checkout.request").with_kind("http"),
    );
    loza::append(&mut ctx, loza::user_id("u_123"));
    loza::enrich(&mut ctx, vec![loza::tenant_id("t_123")]);
    loza::set(&mut ctx, "payment.provider", "stripe");

    let mut merged = Map::new();
    merged.insert("cart.items".to_string(), serde_json::json!(3));
    loza::merge(&mut ctx, merged);

    assert_eq!(
        loza::get_group(&ctx, "payment")
            .and_then(|v| v.get("provider"))
            .and_then(|v| v.as_str()),
        Some("stripe")
    );
    assert_eq!(
        loza::get_group(&ctx, "cart")
            .and_then(|v| v.get("items"))
            .and_then(|v| v.as_i64()),
        Some(3)
    );

    loza::delete(&mut ctx, "payment.provider");
    loza::checkpoint(&mut ctx, "validated");
    loza::checkpoint_with_attrs(
        &mut ctx,
        "payment_started",
        &[loza::string("stage", "payment")],
    );

    let cloned = loza::clone_event(&ctx);
    assert_eq!(cloned.event_id, ctx.event_id);
    loza::link_event(&mut ctx, "evt_parent");

    loza::finish(&mut ctx);
    loza::emit(&mut ctx).expect("emit");
    assert_eq!(ctx.outcome, Some("success".to_string()));

    let mut dropped = loza::start_event(None, loza::Params::new("drop.event"));
    loza::drop(&mut dropped, "capacity");
    let mut cancelled = loza::start_event(None, loza::Params::new("cancel.event"));
    loza::cancel(&mut cancelled);
    let mut abandoned = loza::start_event(None, loza::Params::new("abandon.event"));
    loza::abandon(&mut abandoned);
    let mut retried = loza::start_event(None, loza::Params::new("retry.event"));
    loza::retry(&mut retried);
    let mut partial = loza::start_event(None, loza::Params::new("partial.event"));
    loza::partial(&mut partial, "timeout");

    assert!(loza::current_event().is_none());
}

#[test]
fn link_event_stores_linked_id_in_links() {
    let mut ctx = loza::start_event(None, loza::Params::new("link.request"));

    // Free-function API must record the link instead of silently dropping the id.
    loza::link_event(&mut ctx, "evt_parent");
    assert_eq!(ctx.links.len(), 1, "link_event must store the link");
    assert_eq!(
        ctx.links[0].get("type").and_then(|v| v.as_str()),
        Some("event")
    );
    assert_eq!(
        ctx.links[0].get("target").and_then(|v| v.as_str()),
        Some("evt_parent"),
        "link must carry the linked event id"
    );

    // The adjacent Logger::link_event implementation must behave identically.
    let logger = loza::test("link-svc");
    logger.link_event(&mut ctx, "evt_second");
    assert_eq!(
        ctx.links.len(),
        2,
        "Logger::link_event must append the link"
    );
    assert_eq!(
        ctx.links[1].get("target").and_then(|v| v.as_str()),
        Some("evt_second")
    );

    // The link must survive into the emitted payload under the spec `links` field.
    let snapshot = loza::SnapshotEvent(&ctx);
    let payload: serde_json::Value =
        serde_json::from_str(&snapshot).expect("snapshot must be valid JSON");
    let links = payload
        .get("links")
        .and_then(|v| v.as_array())
        .expect("links must be serialized when non-empty");
    assert_eq!(links.len(), 2);
    assert!(links
        .iter()
        .any(|l| l.get("target").and_then(|v| v.as_str()) == Some("evt_parent")));
    assert!(links
        .iter()
        .any(|l| l.get("target").and_then(|v| v.as_str()) == Some("evt_second")));

    // Linking must not fabricate an error entry.
    assert!(ctx.error.is_none());
}
