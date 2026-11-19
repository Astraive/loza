use std::collections::VecDeque;
use std::fs::{self};
use std::mem;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};

/// Monotonic id used to give every disk-buffer file a unique name, even when
/// several events are enqueued within the same millisecond or from different
/// `DiskOfflineBuffer` instances in this process.
static NEXT_EVENT_ID: AtomicU64 = AtomicU64::new(0);

pub trait WritableSink {
    fn write(&self, encoded: &str) -> Result<(), String>;
}

pub trait BatchWritableSink {
    fn write_batch(&self, encoded_events: &[String]) -> Result<(), String>;
}

#[derive(Clone, Debug, Default)]
pub struct DeliveryStats {
    pub enqueued: u64,
    pub emitted: u64,
    pub dropped: u64,
    pub failed: u64,
    pub retried: u64,
    pub batches: u64,
    pub last_error: String,
}

impl DeliveryStats {
    pub fn snapshot(&self) -> Self {
        self.clone()
    }
}

pub struct ByteBatcher {
    max_batch_bytes: usize,
    max_events: usize,
    current_bytes: usize,
    events: Vec<String>,
}

impl ByteBatcher {
    pub fn new(max_batch_bytes: usize) -> Self {
        Self::with_limits(max_batch_bytes, 1024)
    }

    pub fn with_limits(max_batch_bytes: usize, max_events: usize) -> Self {
        Self {
            max_batch_bytes: max_batch_bytes.max(1),
            max_events: max_events.max(1),
            current_bytes: 0,
            events: Vec::new(),
        }
    }

    pub fn push(&mut self, event: String) -> Option<Vec<String>> {
        let size = event.len();
        let should_flush = self.current_bytes > 0
            && (self.current_bytes + size > self.max_batch_bytes
                || self.events.len() >= self.max_events);
        if should_flush {
            let flushed = self.drain();
            self.events.push(event);
            self.current_bytes = size;
            Some(flushed)
        } else {
            self.current_bytes += size;
            self.events.push(event);
            None
        }
    }

    pub fn drain(&mut self) -> Vec<String> {
        let out = mem::take(&mut self.events);
        self.current_bytes = 0;
        out
    }

    pub fn len(&self) -> usize {
        self.events.len()
    }

    pub fn is_empty(&self) -> bool {
        self.events.is_empty()
    }
}

pub struct MemoryOfflineBuffer {
    queue: Arc<Mutex<VecDeque<String>>>,
    max_size: usize,
}

impl MemoryOfflineBuffer {
    pub fn new(max_size: usize) -> Self {
        Self {
            queue: Arc::new(Mutex::new(VecDeque::new())),
            max_size,
        }
    }

    pub fn enqueue(&self, event: String) -> bool {
        let mut q = self.queue.lock().unwrap();
        if q.len() >= self.max_size {
            q.pop_front();
            q.push_back(event);
            false
        } else {
            q.push_back(event);
            true
        }
    }

    pub fn drain(&self) -> Vec<String> {
        let mut q = self.queue.lock().unwrap();
        q.drain(..).collect()
    }

    pub fn len(&self) -> usize {
        self.queue.lock().unwrap().len()
    }

    pub fn is_empty(&self) -> bool {
        self.queue.lock().unwrap().is_empty()
    }
}

pub struct DiskOfflineBuffer {
    dir: PathBuf,
    max_files: usize,
    /// Serializes `enqueue`, `drain` and `evict_old_files` so a concurrent
    /// drain can never observe a file that is partly written or being
    /// removed (issue #331).
    lock: Mutex<()>,
}

impl DiskOfflineBuffer {
    pub fn new(dir: impl AsRef<Path>, max_files: usize) -> Self {
        let dir = dir.as_ref().to_path_buf();
        fs::create_dir_all(&dir).ok();
        Self {
            dir,
            max_files,
            lock: Mutex::new(()),
        }
    }

    pub fn enqueue(&self, event: &str) -> std::io::Result<()> {
        let _guard = self.lock.lock().unwrap();
        loop {
            let sequence = NEXT_EVENT_ID.fetch_add(1, Ordering::Relaxed);
            // Keep the timestamp first so the lexicographic ordering in `drain`
            // still yields chronological order; the sequence makes the name
            // unique when several events land in the same millisecond.
            let filename = format!(
                "{:020}-{:012}.json",
                crate::internal::clock::unix_millis(),
                sequence
            );
            let path = self.dir.join(filename);

            // A colliding name (e.g. a stale file left by another process)
            // must never be silently overwritten. Retry with the next
            // sequence number instead, mirroring an exclusive-create guard.
            if path.exists() {
                continue;
            }

            // `fs::write` truncates the destination before writing it, so a
            // concurrent reader can observe an empty or half-written event.
            // Write to a temporary file in the same directory and rename it
            // into place atomically instead. The temporary file uses a
            // non-`.json` extension so `drain` ignores it even if a write
            // fails partway.
            let temp_path = self
                .dir
                .join(format!(".{}-{}.tmp", std::process::id(), sequence));
            fs::write(&temp_path, event)?;
            if let Err(err) = fs::rename(&temp_path, &path) {
                let _ = fs::remove_file(&temp_path);
                return Err(err);
            }
            break;
        }

        self.evict_old_files();
        Ok(())
    }

    pub fn drain(&self) -> std::io::Result<Vec<String>> {
        let _guard = self.lock.lock().unwrap();
        let mut events = Vec::new();
        let mut entries: Vec<_> = fs::read_dir(&self.dir)?
            .filter_map(|e| e.ok())
            .filter(|e| {
                e.path()
                    .extension()
                    .map(|ext| ext == "json")
                    .unwrap_or(false)
            })
            .collect();
        entries.sort_by_key(|e| e.file_name());
        for entry in entries {
            let path = entry.path();
            match fs::read_to_string(&path) {
                Ok(content) => {
                    events.push(content);
                    fs::remove_file(path).ok();
                }
                Err(_) => continue,
            }
        }
        Ok(events)
    }

    fn evict_old_files(&self) {
        let mut entries: Vec<_> = match fs::read_dir(&self.dir) {
            Ok(rd) => rd
                .filter_map(|e| e.ok())
                .filter(|e| {
                    e.path()
                        .extension()
                        .map(|ext| ext == "json")
                        .unwrap_or(false)
                })
                .collect(),
            Err(_) => return,
        };
        if entries.len() <= self.max_files {
            return;
        }
        entries.sort_by_key(|e| e.file_name());
        let to_remove = entries.len() - self.max_files;
        for entry in entries.into_iter().take(to_remove) {
            fs::remove_file(entry.path()).ok();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashSet;
    use std::sync::atomic::AtomicBool;
    use std::thread;

    static TEST_DIR_ID: AtomicU64 = AtomicU64::new(0);

    fn test_dir(tag: &str) -> PathBuf {
        std::env::temp_dir().join(format!(
            "loza-disk-buffer-{}-{}-{}",
            std::process::id(),
            tag,
            TEST_DIR_ID.fetch_add(1, Ordering::Relaxed)
        ))
    }

    /// A large, self-describing payload. Because it is big, a reader that
    /// catches a file after truncation but before the write completes sees a
    /// truncated string that is not in the expected set and is detected as
    /// corruption.
    fn payload(producer: usize, seq: usize) -> String {
        format!("P{producer}S{seq}|{}|END", "x".repeat(32 * 1024))
    }

    #[test]
    fn enqueue_writes_atomically_and_leaves_no_temporary_files() {
        let dir = test_dir("atomic");
        let buffer = DiskOfflineBuffer::new(&dir, 10);
        buffer.enqueue("complete-event").unwrap();

        let entries: Vec<_> = std::fs::read_dir(&dir)
            .unwrap()
            .map(|entry| entry.unwrap())
            .collect();
        assert_eq!(entries.len(), 1);
        assert_eq!(
            entries[0].path().extension().and_then(|ext| ext.to_str()),
            Some("json"),
            "only a final .json file should remain after enqueue"
        );
        assert_eq!(buffer.drain().unwrap(), vec!["complete-event"]);
        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn concurrent_enqueue_and_drain_never_yield_partial_events() {
        let dir = test_dir("concurrent");
        let buffer = Arc::new(DiskOfflineBuffer::new(&dir, 10_000));

        let producers = 4usize;
        let per_producer = 40usize;
        let expected: HashSet<String> = (0..producers)
            .flat_map(|p| (0..per_producer).map(move |s| payload(p, s)))
            .collect();

        let done = Arc::new(AtomicBool::new(false));

        // Consumer: drain continuously while producers are running, then do
        // a final full drain once they have all finished.
        let consumer_buffer = Arc::clone(&buffer);
        let consumer_done = Arc::clone(&done);
        let consumer = thread::spawn(move || {
            let mut collected = Vec::new();
            while !consumer_done.load(Ordering::Acquire) {
                let mut events = consumer_buffer.drain().unwrap_or_default();
                collected.append(&mut events);
                thread::yield_now();
            }
            loop {
                let mut events = consumer_buffer.drain().unwrap_or_default();
                if events.is_empty() {
                    break;
                }
                collected.append(&mut events);
            }
            collected
        });

        let mut handles = Vec::new();
        for p in 0..producers {
            let buffer = Arc::clone(&buffer);
            handles.push(thread::spawn(move || {
                for s in 0..per_producer {
                    buffer.enqueue(&payload(p, s)).unwrap();
                }
            }));
        }
        for handle in handles {
            handle.join().unwrap();
        }
        done.store(true, Ordering::Release);
        let collected = consumer.join().unwrap();

        for event in &collected {
            assert!(
                expected.contains(event),
                "drain returned a partial/corrupt event of length {}",
                event.len()
            );
        }
        assert_eq!(collected.len(), producers * per_producer);
        let unique: HashSet<_> = collected.iter().collect();
        assert_eq!(unique.len(), collected.len(), "duplicate events drained");

        std::fs::remove_dir_all(&dir).ok();
    }
}
