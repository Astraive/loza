use std::path::PathBuf;

pub fn default_storage_dir() -> PathBuf {
    std::env::temp_dir().join("loza")
}

/// Return the spool file path for `name`.
///
/// `name` is a single filename stem, never a path. Characters that could
/// alter the path -- separators (`/` and `\`), drive prefixes (`:`), `..`
/// dot segments, and control characters -- are replaced with `_`. This keeps
/// the result directly inside [`default_storage_dir`] even when `name` is
/// absolute (e.g. `/etc/x`) or contains traversal segments
/// (e.g. `../../etc/x`).
pub fn spool_path(name: &str) -> PathBuf {
    let root = default_storage_dir();
    let stem = sanitize_stem(name);
    let path = root.join(format!("{stem}.spool"));

    // Defense in depth: the sanitized stem must resolve to exactly one normal
    // component directly beneath the storage root. Fall back to a fixed safe
    // name if that invariant is ever violated.
    if path.parent() != Some(root.as_path()) {
        return root.join("spool.spool");
    }
    path
}

fn sanitize_stem(name: &str) -> String {
    let stem: String = name
        .trim()
        .chars()
        .map(|c| match c {
            '/' | '\\' | ':' | '.' => '_',
            c if c.is_control() => '_',
            c => c,
        })
        .collect();
    if stem.is_empty() {
        "spool".to_string()
    } else {
        stem
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn assert_contained(path: &PathBuf) {
        let root = default_storage_dir();
        assert_eq!(
            path.parent(),
            Some(root.as_path()),
            "spool path {path:?} escaped storage root {root:?}"
        );
        assert!(path.file_name().is_some(), "spool path has no file name");
    }

    #[test]
    fn plain_name_stays_inside_storage_root() {
        let path = spool_path("events");
        assert_contained(&path);
        assert_eq!(path.file_name().unwrap(), "events.spool");
    }

    #[test]
    fn absolute_name_is_neutralized() {
        let path = spool_path("/etc/passwd");
        assert_contained(&path);
        assert_eq!(path.file_name().unwrap(), "_etc_passwd.spool");
    }

    #[test]
    fn traversal_names_are_neutralized() {
        for name in ["../../etc/passwd", "..", "../x", "a/../b", "a\\..\\b", "C:\\windows\\x"] {
            let path = spool_path(name);
            assert_contained(&path);
        }
    }

    #[test]
    fn empty_and_dot_names_get_safe_stem() {
        for name in ["", ".", "..", "   "] {
            let path = spool_path(name);
            assert_contained(&path);
            assert!(path.file_name().unwrap().to_string_lossy().ends_with(".spool"));
        }
    }
}
