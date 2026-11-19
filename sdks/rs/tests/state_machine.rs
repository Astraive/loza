use loza::{Attr, Config, EventContext, LozaError, New, Params, SinkConfig};

#[test]
fn duplicate_emit_is_idempotent_and_finish_after_emit_are_typed() {
    let logger = New(Config::test("checkout").with_sink(SinkConfig::Noop));
    let mut ctx = logger.start_event(Params::new("state.machine"));
    logger.finish(&mut ctx, "success").unwrap();
    let first = logger.emit(&ctx).unwrap();
    let second = logger.emit(&ctx).unwrap();
    assert_eq!(first, second);
    assert!(matches!(
        logger.finish(&mut ctx, "success"),
        Err(LozaError::EventClosed { .. })
    ));
}

#[test]
fn validation_failure_does_not_mark_emitted() {
    let logger = New(Config::test("checkout"));
    let ctx = EventContext::new("", Params::new("bad"));
    assert!(matches!(logger.emit(&ctx), Err(LozaError::Validation(_))));
    assert!(!ctx.is_emitted());
    assert_eq!(ctx.lifecycle_state(), "failed_validation");
}

#[test]
fn created_transitions_to_active_on_enrich() {
    let logger = New(Config::test("checkout").with_sink(SinkConfig::Noop));
    let mut ctx = logger.start_event(Params::new("state.transition"));
    assert_eq!(ctx.lifecycle_state(), "created");
    logger.enrich(&mut ctx, "user.id", "u1");
    assert_eq!(ctx.lifecycle_state(), "active");
}

fn emitted_context() -> EventContext {
    let logger = New(Config::test("checkout").with_sink(SinkConfig::Noop));
    let mut ctx = logger.start_event(Params::new("closed.mutation"));
    logger.finish(&mut ctx, "success").unwrap();
    logger.emit(&ctx).unwrap();
    assert_eq!(ctx.lifecycle_state(), "emitted");
    ctx
}

#[test]
fn event_mutators_are_no_ops_on_an_emitted_event() {
    let mut ctx = emitted_context();
    let before = serde_json::to_value(&ctx).unwrap();

    ctx.set_attr("foo", "bar");
    ctx.append_attr(Attr::new("appended", 1));
    ctx.merge_group("user", "id", "u1");
    ctx.delete_attr("anything");
    ctx.checkpoint_with_attrs("cp", &[Attr::new("k", "v")]);
    let _process = ctx.start_process("p");
    let _timer = ctx.start_timer("t");
    let _group = ctx.start_group("g");

    assert_eq!(serde_json::to_value(&ctx).unwrap(), before);
    assert_eq!(ctx.lifecycle_state(), "emitted");
}

#[test]
fn logger_mutators_are_no_ops_on_an_emitted_event() {
    let logger = New(Config::test("checkout").with_sink(SinkConfig::Noop));
    let mut ctx = logger.start_event(Params::new("closed.logger.mutation"));
    logger.finish(&mut ctx, "success").unwrap();
    logger.emit(&ctx).unwrap();
    let before = serde_json::to_value(&ctx).unwrap();

    logger.enrich(&mut ctx, "foo", "bar");
    logger.append(&mut ctx, Attr::new("appended", 1));
    logger.merge(&mut ctx, "user", "id", "u1");
    logger.delete(&mut ctx, "anything");
    logger.checkpoint_with_attrs(&mut ctx, "cp", &[]);

    assert_eq!(serde_json::to_value(&ctx).unwrap(), before);
}

#[test]
fn mutators_are_no_ops_on_a_validation_failed_event() {
    let logger = New(Config::test("checkout"));
    let mut ctx = EventContext::new("", Params::new("bad"));
    assert!(logger.emit(&ctx).is_err());
    assert_eq!(ctx.lifecycle_state(), "failed_validation");
    let before = serde_json::to_value(&ctx).unwrap();

    ctx.set_attr("foo", "bar");

    assert_eq!(serde_json::to_value(&ctx).unwrap(), before);
}
