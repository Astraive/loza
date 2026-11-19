package storage

import (
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"time"
)

// NDJSONArchiveKey returns a partitioned object-storage key for NDJSON archives.
func NDJSONArchiveKey(prefix string, ts time.Time) string {
	return archiveObjectKey(prefix, "events", "ndjson", ts)
}

// ParquetArchiveKey returns a partitioned object-storage key for Parquet archives.
func ParquetArchiveKey(prefix, table string, ts time.Time) string {
	return archiveObjectKey(prefix, sanitizeSegment(table), "parquet", ts)
}

// LocalParquetExportPath returns a partitioned filesystem path for Parquet exports.
func LocalParquetExportPath(baseDir, table string, ts time.Time) string {
	utc := ts.UTC()
	return filepath.Join(
		baseDir,
		"table="+sanitizeSegment(table),
		"date="+utc.Format("2006-01-02"),
		"hour="+utc.Format("15"),
		fmt.Sprintf("part-%s.parquet", unixNanoString(utc)),
	)
}

func archiveObjectKey(prefix, dataset, ext string, ts time.Time) string {
	utc := ts.UTC()
	return fmt.Sprintf(
		"%sdataset=%s/date=%s/hour=%s/part-%s.%s",
		normalizePrefix(prefix),
		sanitizeSegment(dataset),
		utc.Format("2006-01-02"),
		utc.Format("15"),
		unixNanoString(utc),
		sanitizeExt(ext),
	)
}

// unixNanoString returns the number of nanoseconds since the Unix epoch as a
// decimal string. Unlike time.Time.UnixNano(), it does not overflow int64 for
// instants outside the representable nanosecond range (before ~1678 or after
// ~2262), so distinct timestamps never collapse onto the same part name due to
// wraparound. The arithmetic is done with big.Int so even the seconds-to-
// nanoseconds multiplication is exact at the extremes of time.Time's range.
func unixNanoString(t time.Time) string {
	nanos := new(big.Int).Mul(big.NewInt(t.Unix()), big.NewInt(int64(time.Second)))
	nanos.Add(nanos, big.NewInt(int64(t.Nanosecond())))
	return nanos.String()
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.ReplaceAll(prefix, "\\", "/")
	segments := strings.Split(prefix, "/")
	clean := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		clean = append(clean, segment)
	}
	if len(clean) == 0 {
		return ""
	}
	return strings.Join(clean, "/") + "/"
}

func sanitizeSegment(seg string) string {
	seg = strings.TrimSpace(seg)
	if seg == "" {
		return "events"
	}
	seg = strings.ReplaceAll(seg, "\\", "_")
	seg = strings.ReplaceAll(seg, "/", "_")
	seg = strings.ReplaceAll(seg, ".", "_")
	return seg
}

func sanitizeExt(ext string) string {
	ext = strings.TrimSpace(ext)
	ext = strings.TrimPrefix(ext, ".")
	if ext == "" {
		return "ndjson"
	}
	ext = strings.ReplaceAll(ext, "\\", "")
	ext = strings.ReplaceAll(ext, "/", "")
	return ext
}
