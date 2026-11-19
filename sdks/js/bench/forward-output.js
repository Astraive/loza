/**
 * Forward a captured subprocess result to this process's output streams and
 * set the resulting exit code.
 *
 * `process.exit()` terminates immediately and can truncate writes to
 * stdout/stderr when they are pipes (CI runners, `tee`, ...), because those
 * writes are asynchronous. Assigning `process.exitCode` instead lets the
 * runtime flush pending output before the process exits naturally.
 */
export function forwardOutput(result, streams = {}) {
  const stdout = streams.stdout ?? process.stdout;
  const stderr = streams.stderr ?? process.stderr;

  if (result.stdout) stdout.write(result.stdout);
  if (result.stderr) stderr.write(result.stderr);

  const exitCode = result.exitCode ?? 1;
  process.exitCode = exitCode;
  return exitCode;
}
