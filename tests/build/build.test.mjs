import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { BUILD_TAGS, artifactName, build, goBuildArgs, inspectGoBuild, metadata, parseArgs, verifyRuntime } from '../../scripts/build.mjs';

test('stable release names, explicit targets and invalid arguments', () => {
  assert.equal(artifactName('windows/amd64'), 'proxyfleet.exe');
  assert.equal(artifactName('linux/amd64'), 'proxyfleet-linux-amd64');
  assert.equal(artifactName('linux/arm64'), 'proxyfleet-linux-arm64');
  assert.deepEqual(parseArgs(['--target','linux/amd64','--target','linux/amd64']).targets, ['linux/amd64']);
  for (const args of [['--target'], ['--target','../bad'], ['--version','v1;echo'], ['--unknown']]) assert.throws(() => parseArgs(args));
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
function buildInfo(target, tags=BUILD_TAGS) {
  const [os,arch]=target.split('/');
  return `binary: go1.25.5\n\tbuild\tGOOS=${os}\n\tbuild\tGOARCH=${arch}\n\tbuild\tCGO_ENABLED=0\n\tbuild\t-tags=${tags.join(',')}\n`;
}

test('compiler and native runtime checks reject incomplete or incorrect builds', () => {
  const args=goBuildArgs('windows/amd64','output with spaces.exe',stamp);
  assert.equal(args[args.indexOf('-tags')+1],BUILD_TAGS.join(' '));
  assert.ok(args[args.indexOf('-ldflags')+1].includes(`buildinfo.BuiltAt=${stamp.built_at}`));
  assert.equal(args[args.indexOf('-o')+1],'output with spaces.exe');
  assert.equal(inspectGoBuild(buildInfo('linux/amd64'),'linux/amd64').go_version,'go1.25.5');
  assert.throws(()=>inspectGoBuild(buildInfo('linux/amd64'),'windows/amd64'), /Incorrect target/);
  assert.throws(()=>inspectGoBuild(buildInfo('linux/amd64',BUILD_TAGS.slice(1)),'linux/amd64'), /Missing/);
  const info={...stamp,goos:'windows',goarch:'amd64',official_release_ready:true,capabilities:Object.fromEntries(BUILD_TAGS.map(tag=>[tag.slice(5),true]))};
  verifyRuntime(info,'windows/amd64',stamp);
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

function runner(failTarget) {
  const targets = new Map();
  return (file,args,options={})=>{
    if (file==='git') return '';
    if (args[0]==='env') return 'linux\narm64'; // Both test outputs are cross builds.
    if (args[0]==='build') {
      const target=`${options.env.GOOS}/${options.env.GOARCH}`;
      assert.equal(options.env.CGO_ENABLED,'0');
      if (target===failTarget) throw new Error('synthetic compiler failure');
      const output=args[args.indexOf('-o')+1];
      fs.writeFileSync(output,`new ${target}`);targets.set(output,target);return '';
    }
    if (args[0]==='version') return buildInfo(targets.get(args[2]));
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
  assert.equal(manifest.artifacts[0].verification,'go-build-info');
  assert.equal(manifest.artifacts[0].runtime_info,undefined);
  assert.equal(fs.readFileSync(path.join(root,'dist/SHA256SUMS.txt'),'utf8'),result.artifacts.map(a=>`${a.sha256}  ${a.file}\n`).join(''));
  assert.equal(fs.readFileSync(path.join(root,'dist/config.yaml'),'utf8'),'preserve runtime data');
  assert.ok(!fs.readdirSync(path.join(root,'dist')).some(name=>name.startsWith('.proxyfleet-build-')));
});
