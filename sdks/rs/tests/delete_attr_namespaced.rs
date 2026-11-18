use loza::{EventContext, Params};

#[test]
fn delete_attr_removes_namespaced_attributes() {
    let mut ctx = EventContext::new("svc", Params::new("test.event"));

    ctx.set_attr("user.email", "a@example.com");
    ctx.set_attr("tenant.id", "tenant-1");
    ctx.set_attr("http.method", "GET");
    ctx.set_attr("resource.name", "orders");
    ctx.set_attr("plain", "value");

    assert!(ctx.user.contains_key("email"));
    assert!(ctx.tenant.contains_key("id"));
    assert!(ctx.http.contains_key("method"));
    assert!(ctx.resource.contains_key("name"));
    assert!(ctx.attrs.contains_key("plain"));

    ctx.delete_attr("user.email");
    ctx.delete_attr("tenant.id");
    ctx.delete_attr("http.method");
    ctx.delete_attr("resource.name");
    ctx.delete_attr("plain");

    assert!(!ctx.user.contains_key("email"));
    assert!(!ctx.tenant.contains_key("id"));
    assert!(!ctx.http.contains_key("method"));
    assert!(!ctx.resource.contains_key("name"));
    assert!(!ctx.attrs.contains_key("plain"));
}

#[test]
fn delete_attr_removes_nested_dotted_keys() {
    let mut ctx = EventContext::new("svc", Params::new("test.event"));

    ctx.set_attr("user.profile.email", "a@example.com");
    ctx.set_attr("custom.deep.value", 42);

    assert_eq!(ctx.user["profile"]["email"].as_str(), Some("a@example.com"));
    assert_eq!(ctx.attrs["custom"]["deep"]["value"].as_i64(), Some(42));

    ctx.delete_attr("user.profile.email");
    ctx.delete_attr("custom.deep.value");

    assert!(ctx.user["profile"].get("email").is_none());
    assert!(ctx.attrs["custom"]["deep"].get("value").is_none());
}

#[test]
fn delete_attr_ignores_missing_keys() {
    let mut ctx = EventContext::new("svc", Params::new("test.event"));

    ctx.set_attr("user.email", "a@example.com");
    ctx.delete_attr("user.missing");
    ctx.delete_attr("tenant.missing");
    ctx.delete_attr("http.missing");
    ctx.delete_attr("resource.missing");
    ctx.delete_attr("custom.missing.deep");

    assert_eq!(ctx.user["email"].as_str(), Some("a@example.com"));
}
