import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { sanitizeArtifacts } from './artifact-sanitizer.mjs';

const credentialPath = process.env.WDE_BROWSER_CREDENTIAL_FILE;
if (!credentialPath) throw new Error('WDE_BROWSER_CREDENTIAL_FILE is required');
const credential = JSON.parse(fs.readFileSync(credentialPath, 'utf8'));
if (typeof credential.api_key !== 'string' || credential.api_key.length < 32) {
  throw new Error('browser credential file is invalid');
}

const directory = path.dirname(fileURLToPath(import.meta.url));
const cli = path.join(directory, 'node_modules', '@playwright', 'test', 'cli.js');
const result = spawnSync(process.execPath, [cli, 'test'], {
  cwd: directory,
  env: process.env,
  stdio: 'inherit',
});

sanitizeArtifacts(path.join(directory, 'artifacts'), [credential.api_key]);
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
