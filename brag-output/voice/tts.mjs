// Generates one narration file per line with Kokoro (npx hyperframes tts), then writes each
// file's length to durations.json for the edit. A line's "say" is a phonetic spelling for the
// voice (espeak reads "Gjallarhorn" as "Jee-jallarhorn"); captions keep "text".
//
//   node voice/tts.mjs [--jobs 3] [id ...]      # all lines, or only the ids given
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const { voice, speed, lines } = JSON.parse(readFileSync(join(here, 'lines.json'), 'utf8'));
const outDir = join(here, '..', 'composition', 'assets', 'vo');
mkdirSync(outDir, { recursive: true });
const args = process.argv.slice(2);
const j = args.indexOf('--jobs');
const jobs = j >= 0 ? Number(args.splice(j, 2)[1]) : 3;
const only = args;
const run = promisify(execFile);
const todo = lines.filter((l) => !only.length || only.includes(l.id));
// Each run loads the model again, so a few run side by side.
async function worker() {
  for (let line = todo.shift(); line; line = todo.shift()) {
    const file = join(outDir, `${line.id}.wav`);
    await run('npx', ['hyperframes', 'tts', JSON.stringify(line.say ?? line.text), '--voice', voice, '--speed', String(speed), '--output', JSON.stringify(file)], {
      shell: process.platform === 'win32',
    });
    console.log(`${line.id}: ${line.text}`);
  }
}
await Promise.all(Array.from({ length: jobs }, worker));

const durations = {};
for (const line of lines) {
  const file = join(outDir, `${line.id}.wav`);
  if (!existsSync(file)) continue;
  const s = execFileSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', file]).toString().trim();
  durations[line.id] = Number(Number(s).toFixed(3));
}
writeFileSync(join(here, 'durations.json'), JSON.stringify(durations, null, 2));
const total = Object.values(durations).reduce((a, b) => a + b, 0);
console.log(`${Object.keys(durations).length} lines, ${total.toFixed(1)} s of speech`);
