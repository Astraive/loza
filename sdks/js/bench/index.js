import { forwardOutput } from './forward-output.js';

const result = Bun.spawnSync(['bunx', 'vitest', 'bench', '--run'], {
  stdout: 'pipe',
  stderr: 'pipe',
});

forwardOutput(result);
