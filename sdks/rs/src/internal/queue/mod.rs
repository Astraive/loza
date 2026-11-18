use std::collections::VecDeque;
use std::fs::{self, OpenOptions};
use std::io::{ErrorKind, Write};
use std::mem;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};

/// Process-wide sequence used to give every queued event its own file name.
static ENQUEUE_SEQ: AtomicU64 = AtomicU64::new(0);

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
}

impl DiskOfflineBuffer {
    pub fn new(dir: impl AsRef<Path>, max_files: usize) -> Self {
        let dir = dir.as_ref().to_path_buf();
        fs::create_dir_all(&dir).ok();
        Self { dir, max_files }
    }

    /// Build a path that is unique even for enqueues in the same millisecond
    /// or from different `DiskOfflineBuffer` instances in this process.
    ///
    /// The timestamp is zero-padded so `drain`'s lexicographic sort keeps the
    /// chronological order, and the sequence disambiguates writes that share
    /// the same millisecond.
    fn next_path(&self) -> PathBuf {
        let millis = crate::internal::clock::unix_millis();
        let seq = ENQUEUE_SEQ.fetch_add(1, Ordering::Relaxed);
        self.dir.join(format!("{:020}-{:016x}.json", millis, seq))
    }

    pub fn enqueue(&self, event: &str) -> std::io::Result<()> {
        // `create_new` is a final guard against silent data loss: if a path
        // ever collides (e.g. with a stale file from another process), retry
        // with the next sequence number instead of truncating that file.
        let mut file = loop {
            let path = self.next_path();
            match OpenOptions::new().write(true).create_new(true).open(path) {
                Ok(file) => break file,
                Err(err) if err.kind() == ErrorKind::AlreadyExists => continue,
                Err(err) => return Err(err),
            }
        };
        file.write_all(event.as_bytes())?;
        drop(file);
        self.evict_old_files();
        Ok(())
    }

    pub fn drain(&self) -> std::io::Result<Vec<String>> {
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
