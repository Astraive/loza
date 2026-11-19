package eventbus

// DLQReasonUnknown is the header value used when a DLQ event is published
// without a concrete reason (a nil error).
const DLQReasonUnknown = "unknown"

// DLQReasonString converts a DLQ reason into a header-safe string. A nil
// error is allowed by the PublishDLQ signature and must not be dereferenced.
func DLQReasonString(reason error) string {
	if reason == nil {
		return DLQReasonUnknown
	}
	return reason.Error()
}
