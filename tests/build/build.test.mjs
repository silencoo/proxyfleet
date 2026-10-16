import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { ROOT, BUILD_TAGS, artifactName, build, goBuildArgs, inspectGoBuild, metadata, parseArgs, verifyRuntime } from '../../scripts/build.mjs';
import { GO_VERSION, GO_TOOLCHAIN, NODE_VERSION, goEnvironment, verifyNodeVersion } from '../../scripts/toolchain.mjs';

test('stable release names, explicit targets and invalid arguments', () => {
  assert.equal(artifactName('windows/amd64'), 'proxyfleet.exe');
  assert.equal(artifactName('linux/amd64'), 'proxyfleet-linux-amd64');
  assert.equal(artifactName('linux/arm64'), 'proxyfleet-linux-arm64');
  assert.equal(artifactName('darwin/arm64'), 'proxyfleet-darwin-arm64');
  assert.equal(artifactName('darwin/amd64'), 'proxyfleet-darwin-amd64');
  assert.deepEqual(parseArgs(['--target','linux/amd64','--target','linux/amd64']).targets, ['linux/amd64']);
  for (const args of [['--target'], ['--target','../bad'], ['--version','v1;echo'], ['--unknown']]) assert.throws(() => parseArgs(args));
});

test('Docker, CI and package metadata use the canonical exact toolchain pins', () => {
  const read = name => fs.readFileSync(path.join(ROOT, name), 'utf8');
  const docker = read('Dockerfile');
  assert.ok(docker.includes(`golang:${GO_VERSION}-bookworm AS builder`));
  assert.ok(docker.includes(`node:${NODE_VERSION}-bookworm-slim AS webui`));
  assert.match(docker, /ENV GOTOOLCHAIN=local/);
  assert.match(docker, /go env GOVERSION/);
  assert.doesNotMatch(read('go.mod'), /^toolchain /m, 'go.mod go directive is the single Go pin');
  const pkg = JSON.parse(read('package.json'));
  assert.equal(pkg.engines.node, NODE_VERSION);
  assert.equal(JSON.parse(read('package-lock.json')).packages[''].engines.node, NODE_VERSION);
  assert.equal(pkg.scripts['build:release'], 'node scripts/build.mjs --target windows/amd64 --target linux/amd64');
  assert.equal(pkg.scripts['prebuild:webui'], 'npm run check:node');
  for (const file of fs.readdirSync(path.join(ROOT, '.github/workflows'))) {
    const workflow = read(`.github/workflows/${file}`);
    if (workflow.includes('actions/setup-go@')) {
      assert.match(workflow, /GOTOOLCHAIN: local/);
      assert.match(workflow, /go-version-file: go\.mod/);
      assert.doesNotMatch(workflow, /go-version:/);
    }
    if (workflow.includes('actions/setup-node@')) {
      assert.match(workflow, /node-version-file: \.node-version/);
      assert.doesNotMatch(workflow, /node-version:/);
    }
  }
});

test('build environment selects the exact compiler and rejects a different Node runtime', () => {
  verifyNodeVersion(NODE_VERSION);
  assert.throws(() => verifyNodeVersion('0.0.0'), /required for official builds/);
  const env = goEnvironment({GOTOOLCHAIN:'auto',GOWORK:'/other/go.work',GOFLAGS:'-race',GOEXPERIMENT:'other',GOOS:'other',GOARCH:'other',GOROOT:'/other/go',GOMODCACHE:'/cache',GOPROXY:'https://proxy.example'});
  assert.equal(env.GOTOOLCHAIN, GO_TOOLCHAIN);
  assert.equal(env.GOWORK, 'off');
  assert.equal(env.GOFLAGS, '');
  assert.equal(env.GOEXPERIMENT, '');
  for (const key of ['GOOS','GOARCH','GOROOT']) assert.equal(env[key], undefined);
  assert.equal(env.GOMODCACHE, '/cache');
  assert.equal(env.GOPROXY, 'https://proxy.example');
});

test('metadata includes untracked changes and supports source archives and reproducible dates', () => {
  const git = args => args[0] === 'rev-parse' ? 'abcdef1' : args[0] === 'status' ? '?? new.go' : 'v1.0.0';
  assert.deepEqual(metadata({},git,new Date(), '0'), {version:'v1.0.0-dirty',commit:'abcdef1',built_at:'1970-01-01T00:00:00Z',dirty:true});
  assert.equal(metadata({version:'v2.0.0'},git,new Date(),'0').version, 'v2.0.0-dirty');
  assert.equal(metadata({},()=>'',new Date(),'0').version, 'dev');
  assert.throws(()=>metadata({},git,new Date(),'invalid'), /SOURCE_DATE_EPOCH/);
  assert.throws(()=>metadata({},()=> 'unsafe value',new Date(),'0'), /unsupported characters/);
});

const stamp = {version:'v1.2.3',commit:'abcdef1',built_at:'2026-01-01T00:00:00Z'};
function buildInfo(target, tags=BUILD_TAGS, compiler=GO_TOOLCHAIN) {
  const [os,arch]=target.split('/');
  return `binary: ${compiler}\n\tbuild\tGOOS=${os}\n\tbuild\tGOARCH=${arch}\n\tbuild\tCGO_ENABLED=0\n\tbuild\t-tags=${tags.join(',')}\n`;
}

test('compiler and native runtime checks reject incomplete or incorrect builds', () => {
  const args=goBuildArgs('windows/amd64','output with spaces.exe',stamp);
  assert.equal(args[args.indexOf('-tags')+1],BUILD_TAGS.join(' '));
  assert.ok(args[args.indexOf('-ldflags')+1].includes(`buildinfo.BuiltAt=${stamp.built_at}`));
  assert.equal(args[args.indexOf('-o')+1],'output with spaces.exe');
  assert.equal(inspectGoBuild(buildInfo('linux/amd64'),'linux/amd64').go_version,GO_TOOLCHAIN);
  assert.throws(()=>inspectGoBuild(buildInfo('linux/amd64',BUILD_TAGS,'go0.0.0'),'linux/amd64'), /Incorrect Go compiler/);
  assert.throws(()=>inspectGoBuild(buildInfo('linux/amd64'),'windows/amd64'), /Incorrect target/);
  assert.throws(()=>inspectGoBuild(buildInfo('linux/amd64',BUILD_TAGS.slice(1)),'linux/amd64'), /Missing/);
  const info={...stamp,go_version:GO_TOOLCHAIN,goos:'windows',goarch:'amd64',official_release_ready:true,capabilities:Object.fromEntries(BUILD_TAGS.map(tag=>[tag.slice(5),true]))};
  verifyRuntime(info,'windows/amd64',stamp);
  assert.throws(()=>verifyRuntime({...info,go_version:'go0.0.0'},'windows/amd64',stamp), /verification failed/);
  assert.throws(()=>verifyRuntime({...info,version:'wrong'},'windows/amd64',stamp), /verification failed/);
  assert.throws(()=>verifyRuntime({...info,capabilities:{}},'windows/amd64',stamp), /verification failed/);
});

function fixture(t) {
  const root=fs.mkdtempSync(path.join(os.tmpdir(),'proxyfleet-build-test-'));
  t.after(()=>{
    assert.equal(path.dirname(root),path.resolve(os.tmpdir()));
    assert.ok(path.basename(root).startsWith('proxyfleet-build-test-'));
    fs.rmSync(root,{recursive:true,force:true});
  });
  fs.mkdirSync(path.join(root,'webui/dist'),{recursive:true});
  fs.writeFileSync(path.join(root,'webui/dist/index.html'),'fixture');
  fs.mkdirSync(path.join(root,'dist'));
  fs.writeFileSync(path.join(root,'dist/proxyfleet.exe'),'old binary');
  fs.writeFileSync(path.join(root,'dist/config.yaml'),'preserve runtime data');
  fs.writeFileSync(path.join(root,'dist/SHA256SUMS.txt'),'old checksums');
  return root;
}

function runner(failTarget, host='linux/arm64') {
  const targets = new Map();
  const stamps = new Map();
  return (file,args,options={})=>{
    if (file==='git') return '';
    if (file === 'go') assert.equal(options.env.GOTOOLCHAIN, GO_TOOLCHAIN);
    if (args[0]==='env') return `${host.replace('/', '\n')}\n${GO_TOOLCHAIN}`;
    if (args[0]==='build') {
      const target=`${options.env.GOOS}/${options.env.GOARCH}`;
      assert.equal(options.env.CGO_ENABLED,'0');
      assert.equal(options.env.GOAMD64,'v1');
      assert.equal(options.env.GOARM64,'v8.0');
      if (target===failTarget) throw new Error('synthetic compiler failure');
      const output=args[args.indexOf('-o')+1];
      fs.writeFileSync(output,`new ${target}`);targets.set(output,target);
      const flags=args[args.indexOf('-ldflags')+1];
      stamps.set(output, {version:flags.match(/\.Version=(\S+)/)[1],commit:flags.match(/\.Commit=(\S+)/)[1],built_at:flags.match(/\.BuiltAt=(\S+)/)[1]});
      return '';
    }
    if (args[0]==='version') return buildInfo(targets.get(args[2]));
    if (args[0]==='--version-json' && targets.get(file)===host) {
      const [goos,goarch]=host.split('/');
      return JSON.stringify({...stamps.get(file),goos,goarch,go_version:GO_TOOLCHAIN,official_release_ready:true,capabilities:Object.fromEntries(BUILD_TAGS.map(tag=>[tag.slice(5),true]))});
    }
    throw new Error('Cross binary must not execute');
  };
}

test('a failed second target preserves published artifacts and runtime data', t=>{
  const root=fixture(t);
  assert.throws(()=>build({targets:['windows/amd64','linux/amd64'],skipWebUI:true},runner('linux/amd64'),root),/synthetic compiler failure/);
  assert.equal(fs.readFileSync(path.join(root,'dist/proxyfleet.exe'),'utf8'),'old binary');
  assert.equal(fs.readFileSync(path.join(root,'dist/SHA256SUMS.txt'),'utf8'),'old checksums');
  assert.equal(fs.readFileSync(path.join(root,'dist/config.yaml'),'utf8'),'preserve runtime data');
  assert.deepEqual(fs.readdirSync(path.join(root,'dist')).sort(),['SHA256SUMS.txt','config.yaml','proxyfleet.exe'].sort());
});

test('successful build publishes checksums and distinguishes cross-build inspection', t=>{
  const root=fixture(t);
  const result=build({targets:['windows/amd64','linux/amd64'],skipWebUI:true},runner(),root);
  assert.equal(result.artifacts.length,2);
  const manifest=JSON.parse(fs.readFileSync(path.join(root,'dist/build-manifest.json'),'utf8'));
  assert.deepEqual(manifest.toolchains, {go:GO_TOOLCHAIN,node:NODE_VERSION});
  assert.equal(manifest.artifacts[0].verification,'go-build-info');
  assert.equal(manifest.artifacts[0].runtime_info,undefined);
  assert.equal(fs.readFileSync(path.join(root,'dist/SHA256SUMS.txt'),'utf8'),result.artifacts.map(a=>`${a.sha256}  ${a.file}\n`).join(''));
  assert.equal(fs.readFileSync(path.join(root,'dist/config.yaml'),'utf8'),'preserve runtime data');
  assert.ok(!fs.readdirSync(path.join(root,'dist')).some(name=>name.startsWith('.proxyfleet-build-')));
});

test('a mismatched compiler stops before touching published artifacts', t=>{
  const root=fixture(t);
  const run=(file,args)=>{
    assert.equal(file,'go');
    assert.equal(args[0],'env');
    return 'darwin\narm64\ngo0.0.0';
  };
  assert.throws(()=>build({targets:[],skipWebUI:true},run,root), /refusing an unpinned build/);
  assert.equal(fs.readFileSync(path.join(root,'dist/proxyfleet.exe'),'utf8'),'old binary');
  assert.equal(fs.readFileSync(path.join(root,'dist/SHA256SUMS.txt'),'utf8'),'old checksums');
});

test('macOS host defaults to a native full-capability build with runtime verification', t=>{
  const root=fixture(t);
  const result=build({targets:[],skipWebUI:true},runner(undefined,'darwin/arm64'),root);
  assert.equal(result.artifacts[0].file,'proxyfleet-darwin-arm64');
  assert.equal(result.artifacts[0].verification,'runtime-and-go-build-info');
  assert.equal(result.artifacts[0].runtime_info.go_version,GO_TOOLCHAIN);
});
