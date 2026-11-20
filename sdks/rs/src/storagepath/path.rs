use std::io;
use std::path::{Path, PathBuf};

/// Returns the default directory used for local spool/storage files.
///
/// The directory is scoped to the current user rather than using a single
/// fixed name in the shared temp dir. This prevents two users on a multi-user
/// host from clobbering each other's spool files, and prevents a local
/// attacker from pre-creating one well-known `/tmp/loza` path to mount a
/// symlink/TOCTOU attack.
///
/// Call [`create_default_storage_dir`] before writing files into this
/// directory so it is created with private permissions and symlink checks.
pub fn default_storage_dir() -> PathBuf {
    std::env::temp_dir().join(format!("loza-{}", user_scope()))
}

/// Return the spool file path for `name`.
///
/// `name` is a single filename stem, never a path. Characters that could
/// alter the path -- separators (`/` and `\`), drive prefixes (`:`), `..`
/// dot segments, and control characters -- are replaced with `_`. This keeps
/// the result directly inside [`default_storage_dir`] even when `name` is
/// absolute (e.g. `/etc/x`) or contains traversal segments
/// (e.g. `../../etc/x`).
/// Creates [`default_storage_dir`] with private permissions and returns it.
///
/// On Unix the directory is created with mode `0700`. If a symlink or any
/// other non-directory entry already exists at the path, the call fails
/// instead of following it.
pub fn create_default_storage_dir() -> io::Result<PathBuf> {
    let dir = default_storage_dir();
    create_private_dir(&dir)?;
    Ok(dir)
}

/// Returns the spool file path for `name` inside the private storage dir.
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

#[cfg(unix)]
fn user_scope() -> String {
    // SAFETY: getuid() takes no arguments and has no preconditions.
    format!("u{}", unsafe { libc::getuid() })
}

#[cfg(not(unix))]
fn user_scope() -> String {
    let raw = std::env::var("USERNAME").unwrap_or_default();
    let sanitized: String = raw
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '-' || c == '_' {
                c
            } else {
                '_'
            }
        })
        .collect();
    if sanitized.is_empty() {
        "default".to_string()
    } else {
        sanitized
    }
}

/// Creates `path` as a private directory, refusing to follow a symlink.
fn create_private_dir(path: &Path) -> io::Result<()> {
    if let Ok(meta) = std::fs::symlink_metadata(path) {
        if meta.file_type().is_symlink() {
            return Err(io::Error::new(
                io::ErrorKind::AlreadyExists,
                "refusing to use a symlink as the Loza storage directory",
            ));
        }
        if !meta.is_dir() {
            return Err(io::Error::new(
                io::ErrorKind::AlreadyExists,
                "Loza storage path exists and is not a directory",
            ));
        }
        // Existing directory: keep it private. If an attacker owns the entry
        // this fails safely rather than widening access.
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o700))?;
        }
        return Ok(());
    }

    // mkdir() does not follow the final path component, so a pre-planted
    // symlink makes create() fail with AlreadyExists instead of being used.
    let mut builder = std::fs::DirBuilder::new();
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt;
        builder.mode(0o700);
    }
    builder.create(path)
}

#[cfg(test)]
mod path_tests {
    use super::*;
    use std::sync::atomic::{AtomicU64, Ordering};

    static NEXT_ID: AtomicU64 = AtomicU64::new(0);


    fn unique_test_path(tag: &str) -> PathBuf {
        std::env::temp_dir().join(format!(
            "loza-path-test-{}-{}-{}",
            std::process::id(),
            NEXT_ID.fetch_add(1, Ordering::Relaxed),
            tag
        ))
    }

    #[test]
    fn default_storage_dir_is_not_a_fixed_global_name() {
        let dir = default_storage_dir();
        assert_ne!(dir, std::env::temp_dir().join("loza"));
        assert_eq!(dir.parent(), Some(std::env::temp_dir().as_path()));
        assert!(
            dir.file_name()
                .and_then(|n| n.to_str())
                .map(|n| n.starts_with("loza-"))
                .unwrap_or(false),
            "storage dir name must be user-scoped: {dir:?}"
        );
    }

    #[test]
    fn default_storage_dir_is_stable_across_calls() {
        assert_eq!(default_storage_dir(), default_storage_dir());
    }

    #[test]
    fn spool_path_is_under_storage_dir() {
        let spool = spool_path("events");
        let dir = default_storage_dir();
        assert_eq!(spool.parent(), Some(dir.as_path()));
        assert!(spool.ends_with("events.spool"));
    }

    #[test]
    fn create_private_dir_uses_restrictive_permissions() {
        let path = unique_test_path("private");
        create_private_dir(&path).expect("create private dir");

        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mode = std::fs::metadata(&path)
                .expect("metadata")
                .permissions()
                .mode()
                & 0o777;
            assert_eq!(mode, 0o700, "expected 0700 permissions, got {mode:o}");
        }

        // A second call must succeed and keep the directory private.
        create_private_dir(&path).expect("idempotent create");

        std::fs::remove_dir_all(&path).expect("cleanup");
    }

    #[cfg(unix)]
    #[test]
    fn create_private_dir_refuses_symlinks() {
        use std::os::unix::fs::symlink;

        let target = unique_test_path("target");
        let link = unique_test_path("link");
        std::fs::create_dir_all(&target).expect("target dir");
        symlink(&target, &link).expect("create symlink");

        let err = create_private_dir(&link).expect_err("symlink must be refused");
        assert_eq!(err.kind(), io::ErrorKind::AlreadyExists);
        assert!(std::fs::symlink_metadata(&link)
            .expect("symlink metadata")
            .file_type()
            .is_symlink());

        std::fs::remove_file(&link).expect("cleanup link");
        std::fs::remove_dir_all(&target).expect("cleanup target");
    }

    #[test]
    fn create_private_dir_refuses_non_directory_entries() {
        let path = unique_test_path("file");
        std::fs::write(&path, b"not a directory").expect("create file");

        let err = create_private_dir(&path).expect_err("regular file must be refused");
        assert_eq!(err.kind(), io::ErrorKind::AlreadyExists);

        std::fs::remove_file(&path).expect("cleanup");
    }
}
