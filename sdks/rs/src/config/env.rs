use super::config::FileConfig;

pub(crate) fn load_env_config() -> FileConfig {
    let dsn = parse_dsn_env(env_string("LOZA_DSN")).unwrap_or(None);
    let dsn_service = dsn
        .as_ref()
        .map(|value| value.service.clone())
        .filter(|value| !value.is_empty());
    let dsn_environment = dsn.as_ref().map(|value| value.env.clone());
    let dsn_collector_name = dsn.as_ref().map(|value| value.collector_name.clone());
    let dsn_username = dsn.as_ref().and_then(|value| value.username.clone());
    let dsn_password = dsn.as_ref().and_then(|value| value.password.clone());
    let dsn_insecure = dsn.as_ref().map(|value| !value.tls);
    FileConfig {
        service: env_string("LOZA_SERVICE_NAME").or(dsn_service),
        version: env_string("LOZA_SERVICE_VERSION"),
        environment: env_string("LOZA_ENVIRONMENT").or(dsn_environment),
        region: env_string("LOZA_REGION"),
        level: env_string("LOZA_LOG_LEVEL"),
        strict: env_bool("LOZA_STRICT"),
        async_enabled: env_bool("LOZA_ASYNC_ENABLED"),
        collector_endpoint: env_string("LOZA_COLLECTOR_URL")
            .or_else(|| env_string("LOZA_COLLECTOR_ENDPOINT"))
            .or_else(|| dsn.as_ref().map(|value| value.base_url.clone())),
        collector_name: dsn_collector_name,
        api_key: env_string("LOZA_API_KEY").or_else(|| env_string("LOZA_COLLECTOR_API_KEY")),
        basic_username: dsn_username,
        basic_password: dsn_password,
        insecure: env_bool("LOZA_INSECURE").or(dsn_insecure),
        duplicate_policy: env_string("LOZA_DUPLICATE_POLICY"),
        max_event_bytes: env_usize("LOZA_MAX_EVENT_BYTES"),
    }
}

/// Resolve the `LOZA_DSN` environment value into a parsed DSN.
///
/// A missing DSN is a valid "no override" state, but a *malformed* DSN is a
/// configuration error. Turning a parse failure into `None` silently (via
/// `.ok()`) hid operator typos and dropped every DSN-derived setting —
/// credentials, collector name, endpoint and TLS mode — with no signal to the
/// operator. Return the `DsnError` so callers can surface it, and emit a
/// diagnostic here because the config loaders cannot return config errors to
/// the user (they fall back to file/default values by design).
fn parse_dsn_env(raw: Option<String>) -> Result<Option<super::dsn::LozaDSN>, super::dsn::DsnError> {
    let Some(raw) = raw else {
        return Ok(None);
    };
    match super::dsn::parse(&raw) {
        Ok(dsn) => Ok(Some(dsn)),
        Err(err) => {
            eprintln!("loza: ignoring malformed LOZA_DSN: {err}");
            Err(err)
        }
    }
}

fn env_string(name: &str) -> Option<String> {
    std::env::var(name)
        .ok()
        .map(|value| value.trim().to_string())
        .filter(|value| !value.is_empty())
}

fn env_bool(name: &str) -> Option<bool> {
    std::env::var(name)
        .ok()
        .and_then(|value| match value.trim().to_ascii_lowercase().as_str() {
            "1" | "true" | "yes" | "on" => Some(true),
            "0" | "false" | "no" | "off" => Some(false),
            _ => None,
        })
}

fn env_usize(name: &str) -> Option<usize> {
    std::env::var(name).ok()?.trim().parse::<usize>().ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn missing_dsn_is_not_an_error() {
        assert!(parse_dsn_env(None).expect("no DSN is valid").is_none());
    }

    #[test]
    fn valid_dsn_env_is_parsed() {
        let dsn = parse_dsn_env(Some("loza://localhost/project?tls=false".into()))
            .expect("valid DSN")
            .expect("parsed DSN");
        assert_eq!(dsn.host, "localhost");
        assert_eq!(dsn.collector_name, "project");
    }

    #[test]
    fn malformed_dsn_env_surfaces_error_instead_of_silent_discard() {
        let err = parse_dsn_env(Some("https://collector.example/project".into()))
            .expect_err("a malformed LOZA_DSN must surface its DsnError, not be silently dropped");
        let message = err.to_string();
        assert!(
            message.contains("invalid Loza DSN"),
            "diagnostic must include the DSN error context: {message}"
        );
        assert!(
            message.contains("scheme must be loza://"),
            "diagnostic must include the parse cause: {message}"
        );
    }
}
