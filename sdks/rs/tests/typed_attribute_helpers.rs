#[test]
fn typed_attribute_helpers_work() {
    assert_eq!(loza::money(4999.0).key, "money");
    assert_eq!(loza::percent(87.5).key, "percent");
    assert_eq!(loza::bytes(2048).key, "bytes");
    assert_eq!(loza::http_status(200).key, "http.status_code");
    assert_eq!(loza::bucket("pro").key, "bucket");
    assert!(loza::masked("secret").sensitive);
    assert_eq!(loza::url("https://example.com").key, "url");
    assert_eq!(loza::email_hash("user@example.com").key, "email.hash");
    assert_eq!(loza::ip_hash("127.0.0.1").key, "ip.hash");
}

#[test]
fn enum_accepts_allowed_value() {
    let attr = loza::Enum("color", "red", vec!["red", "green", "blue"]);
    assert_eq!(attr.key, "color");
    assert_eq!(attr.value, serde_json::json!("red"));

    let alias = loza::enum_("color", "green", vec!["red", "green", "blue"]);
    assert_eq!(alias.key, "color");
    assert_eq!(alias.value, serde_json::json!("green"));
}

#[test]
#[should_panic(expected = "is not one of")]
fn enum_rejects_value_outside_allowed_set() {
    let _ = loza::Enum("color", "purple", vec!["red", "green", "blue"]);
}

#[test]
#[should_panic(expected = "is not one of")]
fn enum_alias_rejects_value_outside_allowed_set() {
    let _ = loza::enum_("color", "purple", vec!["red", "green", "blue"]);
}

#[test]
fn enum_without_allowed_values_remains_unvalidated() {
    let attr = loza::Enum("color", "purple", Vec::<String>::new());
    assert_eq!(attr.key, "color");
    assert_eq!(attr.value, serde_json::json!("purple"));

    let alias = loza::enum_("color", "purple", Vec::<String>::new());
    assert_eq!(alias.key, "color");
    assert_eq!(alias.value, serde_json::json!("purple"));
}
