import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { sanitizeArtifacts } from './artifact-sanitizer.mjs';

test('redige texto e remove binário que contenham o canário protegido', (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'wde-artifact-sanitizer-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const canary = 'wde_test_artifact_canary_value';
  const report = path.join(root, 'error-context.md');
  const binary = path.join(root, 'failure.bin');
  fs.writeFileSync(report, `input value: ${canary}`);
  fs.writeFileSync(binary, Buffer.concat([Buffer.from([0, 1, 2]), Buffer.from(canary)]));

  sanitizeArtifacts(root, [canary]);

  assert.match(fs.readFileSync(report, 'utf8'), /\[REDACTED\]/);
  assert.equal(fs.readFileSync(report).includes(Buffer.from(canary)), false);
  assert.equal(fs.existsSync(binary), false);
});
