//! Load SDK version from loza-rs.yaml metadata file.
//!
//! Falls back to a hardcoded default if the file cannot be found or parsed.
const FALLBACK_VERSION: &str = "0.4.1";

/// Parse only the top-level `version` key from a loza-rs.yaml document.
///
/// Deserializing into a struct keeps unrelated (for example nested) `version:`
/// keys from being mistaken for the SDK version.
fn parse_version(content: &str) -> Option<String> {
    #[derive(serde::Deserialize)]
    struct VersionMetadata {
        version: Option<serde_yaml::Value>,
    }

    let metadata: VersionMetadata = serde_yaml::from_str(content).ok()?;
    let raw = match metadata.version? {
        serde_yaml::Value::String(value) => value,
        serde_yaml::Value::Number(value) => value.to_string(),
        _ => return None,
    };

    let version = raw
        .trim()
        .trim_matches(|c| c == '"' || c == '\'')
        .to_string();
    if version.is_empty() {
        None
    } else {
        Some(version)
    }
}

/// Read the version from the metadata file located relative to `base`.
fn version_from_dir(base: &std::path::Path) -> Option<String> {
    let path = base.join("loza-rs.yaml");
    let content = std::fs::read_to_string(path).ok()?;
    parse_version(&content)
}

/// Read version from loza-rs.yaml, located relative to the crate manifest.
/// Returns FALLBACK_VERSION if file not found or parsing fails.
fn load_version() -> String {
    // Use the manifest directory instead of the process CWD: at runtime the
    // CWD belongs to the consumer, not to this crate.
    let manifest_dir = std::path::Path::new(env!("CARGO_MANIFEST_DIR"));
    version_from_dir(manifest_dir).unwrap_or_else(|| FALLBACK_VERSION.to_string())
}

/// SDK version loaded from loza-rs.yaml at startup.
pub fn sdk_version() -> &'static str {
    // Use a once_cell-like pattern for thread-safe lazy initialization
    use std::sync::OnceLock;
    static VERSION: OnceLock<String> = OnceLock::new();
    VERSION.get_or_init(load_version)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_version_reads_only_the_top_level_version_key() {
        let yaml = "\
name: loza-rs
version: 1.2.3
metadata:
  version: 9.9.9
publish:
  version: 7.7.7
";
        assert_eq!(parse_version(yaml).as_deref(), Some("1.2.3"));
    }

    #[test]
    fn parse_version_ignores_nested_only_version_keys() {
        let yaml = "\
name: loza-rs
metadata:
  version: 9.9.9
publish:
  version: 7.7.7
";
        assert_eq!(parse_version(yaml), None);
    }

    #[test]
    fn parse_version_accepts_quoted_and_numeric_versions() {
        assert_eq!(
            parse_version("version: \"2.0.0\"\n").as_deref(),
            Some("2.0.0")
        );
        assert_eq!(parse_version("version: 0.4\n").as_deref(), Some("0.4"));
    }

    #[test]
    fn version_from_dir_uses_the_given_base_directory() {
        let temp = std::env::temp_dir().join(format!("loza-version-base-{}", std::process::id()));
        let _ = std::fs::create_dir_all(&temp);
        std::fs::write(temp.join("loza-rs.yaml"), "version: 3.2.1\n").unwrap();

        let observed = version_from_dir(&temp);

        let _ = std::fs::remove_dir_all(&temp);
        assert_eq!(observed.as_deref(), Some("3.2.1"));
    }

    #[test]
    fn load_version_is_independent_of_process_cwd() {
        // A conflicting metadata file in the current working directory must
        // not override the one located relative to the crate manifest.
        static CWD_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());
        let _guard = CWD_LOCK.lock().unwrap();

        let original = std::env::current_dir().unwrap();
        let temp = std::env::temp_dir().join(format!("loza-version-cwd-{}", std::process::id()));
        let _ = std::fs::create_dir_all(&temp);
        std::fs::write(temp.join("loza-rs.yaml"), "version: 9.9.9\n").unwrap();

        std::env::set_current_dir(&temp).unwrap();
        let observed = load_version();
        std::env::set_current_dir(&original).unwrap();

        let _ = std::fs::remove_dir_all(&temp);

        let expected = version_from_dir(std::path::Path::new(env!("CARGO_MANIFEST_DIR")))
            .unwrap_or_else(|| FALLBACK_VERSION.to_string());
        assert_eq!(observed, expected);
        assert_ne!(observed, "9.9.9");
    }
}
