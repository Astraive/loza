/**
 * Load SDK version from loza-js.yaml metadata file.
 *
 * Falls back to a hardcoded default if the file cannot be found or parsed.
 * Node built-ins are imported dynamically inside the guarded loader below so
 * browser / non-Node runtimes never fail during module evaluation.
 */

const FALLBACK_VERSION = '0.4.1';

/** Extract the `version:` value from loza-js.yaml content. */
function parseVersion(content: string): string | undefined {
  for (const line of content.split('\n')) {
    const trimmed = line.trim();
    if (trimmed.startsWith('version:')) {
      const value = trimmed.slice('version:'.length).trim().replace(/^["']|["']$/g, '');
      if (value) return value;
    }
  }
  return undefined;
}

/**
 * Read version from loza-js.yaml, searching standard locations.
 *
 * The `node:*` specifiers are imported dynamically and inside the `try` block.
 * A static import would resolve during module evaluation, before this guard
 * could run, so an unavailable built-in would crash non-Node consumers.
 */
async function loadVersion(): Promise<string> {
  try {
    const [{ existsSync, readFileSync }, { resolve, join, dirname }, { fileURLToPath }] =
      await Promise.all([
        import('node:fs'),
        import('node:path'),
        import('node:url'),
      ]);

    let packageRoot: string;
    try {
      const here = dirname(fileURLToPath(import.meta.url));
      packageRoot = resolve(here, '..', '..');
    } catch {
      packageRoot = process.cwd();
    }

    const candidates = [
      join(packageRoot, 'loza-js.yaml'),
      join(process.cwd(), 'loza-js.yaml'),
    ];

    for (const path of candidates) {
      if (existsSync(path)) {
        try {
          const version = parseVersion(readFileSync(path, 'utf-8'));
          if (version) return version;
        } catch {
          // Read failed, try next candidate
        }
      }
    }
  } catch {
    // Browser/non-Node environment: Node built-ins are unavailable.
  }

  return FALLBACK_VERSION;
}

/** SDK version loaded from loza-js.yaml at module init time. */
export const SDK_VERSION: string = await loadVersion();
