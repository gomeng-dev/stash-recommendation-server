import { execFileSync } from 'node:child_process';

const diff = execFileSync('git', ['diff', '--cached', '--unified=0'], { encoding: 'utf8' });
const patterns = [
  { name: 'Stash API key assignment', pattern: /^\+.*STASH_API_KEY\s*=\s*[^\s#]+/i },
  { name: 'Generic API key', pattern: /^\+.*api[_-]?key\s*[:=]\s*['\"]?[A-Za-z0-9_\-]{16,}/i },
  { name: 'Bearer token', pattern: /^\+.*Bearer\s+[A-Za-z0-9._\-]{20,}/i },
  { name: 'Private key', pattern: /^\+.*BEGIN (RSA |OPENSSH |EC )?PRIVATE KEY/ },
];

const hits = [];
for (const line of diff.split('\n')) {
  if (!line.startsWith('+') || line.startsWith('+++')) continue;
  for (const { name, pattern } of patterns) {
    if (pattern.test(line)) hits.push(`${name}: ${line.slice(0, 160)}`);
  }
}

if (hits.length > 0) {
  console.error('Potential secrets in added lines:');
  for (const hit of hits) console.error(`- ${hit}`);
  process.exit(1);
}

console.log('No added-line secret patterns found.');
