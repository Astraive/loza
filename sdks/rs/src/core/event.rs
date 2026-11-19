pub use crate::event::{Attr, ContextCarrier, ContextSource, EventContext, Params};

use serde_json::{Map, Value};

pub fn group(name: impl Into<String>, attrs: impl IntoIterator<Item = Attr>) -> Attr {
    let mut object = Map::new();
    for attr in attrs {
        object.insert(attr.key, attr.value);
    }
    Attr::new(name.into(), Value::Object(object))
}

pub fn sensitive_string(key: impl Into<String>, value: impl Into<String>) -> Attr {
    Attr::new(key.into(), value.into()).sensitive()
}

pub fn hash_string(key: impl Into<String>, value: impl Into<String>) -> Attr {
    Attr::new(key.into(), value.into()).hash_value()
}

/// Return the event context unchanged.
///
/// Unlike the other Loza SDKs, this helper receives an `&EventContext` directly
/// rather than an opaque context object, so an event is always present. The old
/// `Option` return type could never be `None` and misled callers into checking
/// `.is_some()`; use [`has_event`] to test whether the event is still active.
pub fn from_context(ctx: &EventContext) -> &EventContext {
    ctx
}

pub fn has_event(ctx: &EventContext) -> bool {
    !ctx.is_emitted()
}

pub fn event_id(ctx: &EventContext) -> &str {
    &ctx.event_id
}

pub fn request_id(ctx: &EventContext) -> &str {
    &ctx.request_id
}

pub fn id(event: &EventContext) -> &str {
    event_id(event)
}

pub fn is_finished(event: &EventContext) -> bool {
    event.is_finished()
}

pub fn is_emitted(event: &EventContext) -> bool {
    event.is_emitted()
}

pub fn checkpoint_names(event: &EventContext) -> Vec<String> {
    event
        .checkpoints
        .iter()
        .filter_map(|c| c.get("name").and_then(Value::as_str).map(str::to_string))
        .collect()
}

pub fn checkpoint_payload(name: &str, at_ms: u64) -> serde_json::Map<String, Value> {
    let mut payload = serde_json::Map::new();
    payload.insert("name".to_string(), Value::String(name.to_string()));
    payload.insert("at_ms".to_string(), Value::Number(at_ms.into()));
    payload
}

pub fn params_from_http(method: &str, path: &str, route: Option<&str>) -> Params {
    let mut params = Params::new(format!("{method} {path}")).with_kind("http");
    params.method = Some(method.to_string());
    params.path = Some(path.to_string());
    params.route = route.map(str::to_string);
    params
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn id_delegates_to_event_id() {
        let mut event = EventContext::new("svc", Params::new("test.event"));

        // Both helpers read the same source of truth.
        assert_eq!(id(&event), event_id(&event));
        assert_eq!(id(&event), event.event_id);

        // A change to the underlying field is observed through both helpers,
        // confirming `id` no longer keeps its own duplicate implementation.
        event.event_id = "evt_updated".to_string();
        assert_eq!(id(&event), "evt_updated");
        assert_eq!(id(&event), event_id(&event));
    }

    #[test]
    fn from_context_returns_the_context_directly() {
        let ctx = EventContext::new("test-svc", Params::new("test.event"));

        // The return type is `&EventContext`, not `Option<&EventContext>`: the
        // event is always present, so callers no longer need an `.is_some()`
        // check that could never fail.
        let returned: &EventContext = from_context(&ctx);

        assert!(std::ptr::eq(returned, &ctx));
        assert_eq!(returned.event_id, ctx.event_id);
        assert_eq!(returned.service, "test-svc");
    }
}
