import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

export const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const BUILD_TAGS = fs.readFileSync(path.join(ROOT, 'scripts/build-tags.txt'), 'utf8').trim().split(/\s+/);
const TARGETS = new Set(['windows/amd64', 'linux/amd64', 'linux/arm64']);
const INFO_PACKAGE = 'github.com/silencoo/proxyfleet/internal/buildinfo';

export function parseArgs(args) {
  const options = { targets: [], skipWebUI: false };
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === '--help' || arg === '-h') options.help = true;
    else if (arg === '--skip-webui') options.skipWebUI = true;
    else if (['--target', '--version', '--commit'].includes(arg)) {
      const value = args[++i];
      if (!value || value.startsWith('--')) throw new Error(`Missing value for ${arg}`);
      if (arg === '--target') {
        artifactName(value); // Reject unsupported targets before doing any work.
        if (!options.targets.includes(value)) options.targets.push(value);
      } else {
        if (!/^[a-zA-Z0-9][a-zA-Z0-9.+_-]*$/.test(value)) throw new Error(`Invalid ${arg} value`);
        options[arg.slice(2)] = value;
      }
    } else throw new Error(`Unknown argument: ${arg}`);
  }
  return options;
}

export function artifactName(target) {
  if (!TARGETS.has(target)) throw new Error(`Unsupported target ${target}; choose ${[...TARGETS].join(', ')}`);
  // Keep the existing release download names compatible with users' scripts.
  return target === 'windows/amd64' ? 'proxyfleet.exe' : `proxyfleet-${target.replace('/', '-')}`;
}

function command(file, args, { capture = false, env = process.env, optional = false } = {}) {
  const result = spawnSync(file, args, { cwd: ROOT, env, encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit' });
  if (result.error || result.status !== 0) {
    if (optional) return '';
    throw new Error(`${file} failed: ${result.error?.message || result.stderr?.trim() || `exit ${result.status}`}`);
  }
  return result.stdout?.trim() || '';
}

function npmRun(script) {
  // npm run supplies its CLI path on every platform, avoiding cmd.exe quoting.
  const npmCLI = process.env.npm_execpath || path.join(path.dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js');
  if (!fs.existsSync(npmCLI)) throw new Error('Run via npm run build (or use --skip-webui for an already built WebUI).');
  command(process.execPath, [npmCLI, 'run', script]);
}

export function metadata(options, git, now = new Date(), sourceDateEpoch = process.env.SOURCE_DATE_EPOCH) {
  const commit = options.commit || git(['rev-parse', 'HEAD']) || 'unknown';
  const status = git(['status', '--porcelain', '--untracked-files=normal']);
  const dirty = status !== '';
  let version = options.version || git(['describe', '--tags', '--always']) || 'dev';
  if (dirty && !version.endsWith('-dirty')) version += '-dirty';
  if (!/^[a-zA-Z0-9][a-zA-Z0-9.+_-]*$/.test(version) || !/^[a-zA-Z0-9][a-zA-Z0-9.+_-]*$/.test(commit)) {
    throw new Error('Version/commit contains unsupported characters; use --version/--commit.');
  }
  if (sourceDateEpoch !== undefined) {
    if (!/^\d+$/.test(sourceDateEpoch)) throw new Error('SOURCE_DATE_EPOCH must be Unix seconds.');
    now = new Date(Number(sourceDateEpoch) * 1000);
  }
  if (!Number.isFinite(now.getTime())) throw new Error('Invalid build timestamp.');
  return { version, commit, built_at: now.toISOString().replace(/\.\d{3}Z$/, 'Z'), dirty };
}

export function goBuildArgs(target, output, stamp) {
  artifactName(target);
  const ldflags = ['-s', '-w', ...['Version', 'Commit', 'BuiltAt'].flatMap((field, i) =>
    ['-X', `${INFO_PACKAGE}.${field}=${[stamp.version, stamp.commit, stamp.built_at][i]}`])].join(' ');
  return ['build', '-buildvcs=false', '-trimpath', '-tags', BUILD_TAGS.join(' '), '-ldflags', ldflags, '-o', output, './cmd/proxyfleet'];
}

export function inspectGoBuild(output, target) {
  const settings = new Map([...output.matchAll(/^\s*build\s+([^=\s]+)=(.*)$/gm)].map(match => [match[1], match[2].trim()]));
  const [os, arch] = target.split('/');
  if (settings.get('GOOS') !== os || settings.get('GOARCH') !== arch || settings.get('CGO_ENABLED') !== '0') {
    throw new Error(`Incorrect target/CGO settings in ${target} binary`);
  }
  const tags = settings.get('-tags')?.split(',') || [];
  if (BUILD_TAGS.some(tag => !tags.includes(tag))) throw new Error(`Missing full-capability tags in ${target} binary`);
  return { go_version: output.split(/\r?\n/)[0].match(/go\d+\S*$/)?.[0], target, build_tags: tags };
}

export function verifyRuntime(info, target, stamp) {
  const [os, arch] = target.split('/');
  if (info.goos !== os || info.goarch !== arch || info.version !== stamp.version || info.commit !== stamp.commit || info.built_at !== stamp.built_at || info.official_release_ready !== true ||
      BUILD_TAGS.some(tag => info.capabilities?.[tag.slice(5)] !== true)) {
    throw new Error(`Runtime build-info verification failed for ${target}`);
  }
}

export function build(options, run = command, root = ROOT) {
  const host = run('go', ['env', 'GOHOSTOS', 'GOHOSTARCH'], { capture: true }).split(/\s+/).join('/');
  const targets = options.targets.length ? options.targets : [host];
  targets.forEach(artifactName);
  const git = args => run('git', ['-c', `safe.directory=${root.replaceAll('\\', '/')}`, ...args], { capture: true, optional: true });
  if (!options.skipWebUI) {
    npmRun('check:webui');
    npmRun('build:webui');
  }
  if (!fs.existsSync(path.join(root, 'webui/dist/index.html'))) throw new Error('Embedded WebUI missing; run npm ci && npm run build.');
  const stamp = metadata(options, git);
  console.log(`Building ProxyFleet ${stamp.version} (${stamp.commit}), ${targets.join(', ')}`);
  const outDir = path.join(root, 'dist');
  fs.mkdirSync(outDir, { recursive: true });
  if (fs.lstatSync(outDir).isSymbolicLink()) throw new Error('dist must be a real directory, not a symlink.');
  const staging = fs.mkdtempSync(path.join(outDir, '.proxyfleet-build-'));
  try {
    const artifacts = [];
    for (const target of targets) {
      const [os, arch] = target.split('/');
      const file = artifactName(target), output = path.join(staging, file);
      console.log(`Compiling ${file}`);
      run('go', goBuildArgs(target, output, stamp), { env: { ...process.env, CGO_ENABLED: '0', GOOS: os, GOARCH: arch } });
      const inspection = inspectGoBuild(run('go', ['version', '-m', output], { capture: true }), target);
      let runtimeInfo;
      if (target === host) {
        runtimeInfo = JSON.parse(run(output, ['--version-json'], { capture: true }));
        verifyRuntime(runtimeInfo, target, stamp);
      }
      const sha256 = createHash('sha256').update(fs.readFileSync(output)).digest('hex');
      artifacts.push({ file, sha256, ...inspection, verification: runtimeInfo ? 'runtime-and-go-build-info' : 'go-build-info', ...(runtimeInfo ? { runtime_info: runtimeInfo } : {}) });
    }
    fs.writeFileSync(path.join(staging, 'SHA256SUMS.txt'), artifacts.map(a => `${a.sha256}  ${a.file}\n`).join(''));
    fs.writeFileSync(path.join(staging, 'build-manifest.json'), JSON.stringify({ ...stamp, artifacts }, null, 2) + '\n');
    // Publish only after every requested target compiles and passes inspection.
    // Never clean dist: it can contain other builds or the user's runtime data.
    for (const file of [...artifacts.map(a => a.file), 'SHA256SUMS.txt', 'build-manifest.json']) {
      fs.renameSync(path.join(staging, file), path.join(outDir, file));
    }
    console.log(`Build complete: ${outDir}\n${artifacts.map(a => `  ${a.file}  ${a.sha256}`).join('\n')}`);
    return { ...stamp, artifacts };
  } finally {
    if (path.dirname(staging) !== outDir || !path.basename(staging).startsWith('.proxyfleet-build-')) throw new Error('Invalid staging cleanup path');
    fs.rmSync(staging, { recursive: true, force: true });
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = parseArgs(process.argv.slice(2));
    if (options.help) console.log('Usage: npm run build -- [--target os/arch] [--version VERSION] [--commit COMMIT] [--skip-webui]\nDefault: host target, full capabilities, rebuild WebUI, output to dist/. Repeat --target for multiple binaries.\nnpm run build:release builds Windows amd64 and Linux amd64 without publishing.');
    else build(options);
  } catch (error) {
    console.error(`Build failed: ${error.message}`);
    process.exitCode = 1;
  }
}
