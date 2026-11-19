use serde_json::Value;

/// Describes a malformed line encountered while parsing newline-delimited JSON.
#[derive(Debug)]
pub struct NdjsonError {
    /// 1-based index of the offending line in the input.
    pub line: usize,
    /// The JSON parsing error produced by `serde_json`.
    pub source: serde_json::Error,
}

impl std::fmt::Display for NdjsonError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "invalid JSON on NDJSON line {}: {}",
            self.line, self.source
        )
    }
}

impl std::error::Error for NdjsonError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        Some(&self.source)
    }
}

pub fn encode_line(value: &Value) -> Result<String, serde_json::Error> {
    Ok(format!("{}\n", serde_json::to_string(value)?))
}

/// Parse newline-delimited JSON values.
///
/// Unlike a `filter_map(...ok())` pass, this surfaces malformed lines to the
/// caller instead of silently dropping them: the first line that is not valid
/// JSON produces an [`NdjsonError`] carrying its 1-based line number. Empty or
/// whitespace-only lines are ignored.
pub fn parse_lines(input: &str) -> Result<Vec<Value>, NdjsonError> {
    input
        .lines()
        .enumerate()
        .filter_map(|(index, line)| {
            if line.trim().is_empty() {
                return None;
            }
            match serde_json::from_str(line) {
                Ok(value) => Some(Ok(value)),
                Err(source) => Some(Err(NdjsonError {
                    line: index + 1,
                    source,
                })),
            }
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_all_valid_lines() {
        let values = parse_lines("{\"a\":1}\n{\"b\":2}\n").expect("valid NDJSON");
        assert_eq!(values.len(), 2);
        assert_eq!(values[0]["a"], 1);
        assert_eq!(values[1]["b"], 2);
    }

    #[test]
    fn malformed_line_is_reported_not_discarded() {
        let result = parse_lines("{\"a\":1}\nnot-json\n{\"b\":2}");
        let error = result.expect_err("malformed line must not be silently dropped");
        assert_eq!(error.line, 2);
    }

    #[test]
    fn blank_lines_are_ignored() {
        let values = parse_lines("{\"a\":1}\n\n   \n{\"b\":2}\n").expect("valid NDJSON");
        assert_eq!(values.len(), 2);
    }

    #[test]
    fn encode_round_trip() {
        let value: Value = serde_json::from_str("{\"x\":\"y\"}").unwrap();
        let encoded = encode_line(&value).unwrap();
        let values = parse_lines(&encoded).unwrap();
        assert_eq!(values, vec![value]);
    }
}
