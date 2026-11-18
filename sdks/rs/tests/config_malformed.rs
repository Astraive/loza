use std::fs;
use std::path::PathBuf;
use std::time::{SystemTime, UNIX_EPOCH};

fn unique_temp_dir() -> PathBuf {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("system time")
        .as_nanos();
    std::env::temp_dir().join(format!("loza-rs-malformed-{}-{nanos}", std::process::id()))
}

#[test]
fn try_from_env_surfaces_malformed_config_error() {
    let tmp = unique_temp_dir();
    fs::create_dir_all(&tmp).expect("mkdir temp");
    let bad = tmp.join("malformed.yaml");
    fs::write(&bad, "strict: [unterminated\n").expect("write malformed config");

    // Point the defaults layer at a missing file (no overrides) and the user
    // layer at a malformed file so the parse failure must be surfaced.
    std::env::set_var("LOZA_RS_DEFAULTS", tmp.join("no-defaults.yaml").as_os_str());
    std::env::set_var("LOZA_RS_CONFIG", bad.as_os_str());

    let result = loza::Config::try_from_env();
    assert!(
        result.is_err(),
        "a malformed config file must not be silently discarded"
    );
    let err = result.expect_err("checked above");
    assert!(
        err.to_string().contains("configuration"),
        "error should mention configuration: {err}"
    );

    std::env::remove_var("LOZA_RS_DEFAULTS");
    std::env::remove_var("LOZA_RS_CONFIG");
    let _ = fs::remove_dir_all(&tmp);
}
