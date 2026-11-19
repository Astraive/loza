import { readFileSync } from 'node:fs';

const coverageFile = process.argv[2] || 'coverage/lcov.info';
const records = readFileSync(coverageFile, 'utf8')
  .split('end_of_record')
  .map(record => record.trim())
  .filter(Boolean);
// Coverage producers disagree on how they emit SF paths: Bun uses "src/foo.ts",
// but others emit "./src/foo.ts", Windows paths ("C:\\repo\\src\\foo.ts"), or
// absolute paths. Normalize to a project-relative "src/..." path so none of
// them are silently dropped by the filter below.
function toProjectRelativeSource(rawSource: string): string | undefined {
  const normalized = rawSource
    .replaceAll('\\', '/')
    .replace(/^[A-Za-z]:/, '')
    .replace(/^\/+/, '');
  const match = normalized.match(/(?:^|\/)src\/(.+)$/);
  return match ? `src/${match[1]}` : undefined;
}

const sourceRecords = records.filter(record => {
  const source = record.match(/^SF:(.+)$/m)?.[1];
  if (!source) return false;
  const relative = toProjectRelativeSource(source);
  return relative?.startsWith('src/') && relative.endsWith('.ts');
});
if (sourceRecords.length === 0) {
  throw new Error(`No production source records found in ${coverageFile}`);
}

let linesFound = 0;
let linesHit = 0;
for (const record of sourceRecords) {
  const totals = [...record.matchAll(/^DA:\d+,(\d+)(?:,[^,]*)?$/gm)].map(match => Number(match[1]));
  linesFound += totals.length;
  linesHit += totals.filter(count => count > 0).length;
}
const percentage = linesFound === 0 ? 0 : (linesHit / linesFound) * 100;
console.log(`Production line coverage: ${linesHit}/${linesFound} (${percentage.toFixed(2)}%)`);
if (percentage < 95) {
  throw new Error(`Production line coverage ${percentage.toFixed(2)}% is below the required 95%`);
}
