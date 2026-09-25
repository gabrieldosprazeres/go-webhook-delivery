import fs from 'node:fs';
import path from 'node:path';

const textExtensions = new Set([
  '.css', '.html', '.js', '.json', '.log', '.md', '.txt', '.xml', '.yaml', '.yml',
]);

function filesUnder(root) {
  if (!fs.existsSync(root)) return [];
  const result = [];
  const pending = [root];
  while (pending.length > 0) {
    const current = pending.pop();
    const stat = fs.lstatSync(current);
    if (stat.isSymbolicLink()) {
      fs.unlinkSync(current);
    } else if (stat.isDirectory()) {
      for (const entry of fs.readdirSync(current)) pending.push(path.join(current, entry));
    } else if (stat.isFile()) {
      result.push(current);
    }
  }
  return result;
}

export function sanitizeArtifacts(root, secrets) {
  const values = [...new Set(secrets.filter((value) => typeof value === 'string' && value.length > 0))];
  if (values.length === 0) throw new Error('artifact sanitizer requires at least one secret');

  for (const file of filesUnder(root)) {
    const raw = fs.readFileSync(file);
    const containsSecret = values.some((secret) => raw.includes(Buffer.from(secret)));
    if (!containsSecret) continue;
    if (!textExtensions.has(path.extname(file).toLowerCase())) {
      fs.unlinkSync(file);
      continue;
    }
    let sanitized = raw.toString('utf8');
    for (const secret of values) sanitized = sanitized.split(secret).join('[REDACTED]');
    fs.writeFileSync(file, sanitized, { mode: 0o600 });
  }

  for (const file of filesUnder(root)) {
    const raw = fs.readFileSync(file);
    if (values.some((secret) => raw.includes(Buffer.from(secret)))) {
      throw new Error(`browser artifact still contains protected material: ${path.basename(file)}`);
    }
  }
}
