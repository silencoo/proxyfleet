import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const GO_VERSION = fs.readFileSync(path.join(ROOT, 'go.mod'), 'utf8').match(/^go (\d+\.\d+\.\d+)$/m)?.[1];
export const NODE_VERSION = fs.readFileSync(path.join(ROOT, '.node-version'), 'utf8').trim();
if (!GO_VERSION || !/^\d+\.\d+\.\d+$/.test(NODE_VERSION)) throw new Error('Pin exact Go and Node patch versions in go.mod and .node-version.');
export const GO_TOOLCHAIN = `go${GO_VERSION}`;

export function verifyNodeVersion(version = process.versions.node) {
  if (version !== NODE_VERSION) throw new Error(`Node ${NODE_VERSION} is required for official builds (found ${version}). Switch using .node-version; your global installation is not modified.`);
}

export function goEnvironment(env = process.env) {
  // An exact GOTOOLCHAIN selects the pinned compiler, even on newer hosts.
  // Ignore ambient workspace/build overrides, but preserve proxy/cache settings.
  const result = { ...env, GOTOOLCHAIN: GO_TOOLCHAIN, GOWORK: 'off', GOFLAGS: '', GOEXPERIMENT: '' };
  for (const key of ['GOOS', 'GOARCH', 'GOROOT']) delete result[key];
  return result;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    verifyNodeVersion();
    console.log(`Node ${NODE_VERSION}; official Go compiler: ${GO_TOOLCHAIN}`);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
