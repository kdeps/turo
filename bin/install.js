#!/usr/bin/env node

// turo installer.
//
// 1. Installs the turo binary to ~/.turo/bin (unless --no-binary).
// 2. Registers the turo skill + slash command with every coding agent
//    detected on this machine, so any agent that can shell out to a binary
//    pipes context through turo automatically.
//
// Usage:
//   npx turo                 install binary + register with detected agents
//   npx turo --list          list every supported agent and its status
//   npx turo --only claude   register with a specific agent only
//   npx turo --all           register with every supported agent
//   npx turo --no-binary     skip the binary download, register agents only
//   npx turo --dry-run       print what would happen, write nothing
//   npx turo --uninstall     remove the binary and registered skills
//   npx turo --help

'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const child_process = require('child_process');

const REPO = 'kdeps/turo';
const VERSION = '0.1.0';
const RELEASE_URL = `https://github.com/${REPO}`;

// -------------------------------------------------------------------------
// Agent registry
//
// mech describes how the skill reaches the agent:
//   claude   native copy into ~/.claude (skill + command)
//   gemini   `gemini extensions install`
//   opencode native copy into ~/.config/opencode
//   skills   `npx -y skills add <repo> --skill turo -a <profile>`
// detect is a `||`-separated list of clauses: command:x, dir:x, file:x,
//   macapp:x, vscode-ext:x, cursor-ext:x.
// -------------------------------------------------------------------------
const AGENTS = [
  { id: 'claude',   label: 'Claude Code',   mech: 'claude',   detect: 'command:claude' },
  { id: 'gemini',   label: 'Gemini CLI',    mech: 'gemini',   detect: 'command:gemini' },
  { id: 'opencode', label: 'opencode',      mech: 'opencode', detect: 'command:opencode' },

  { id: 'codex',    label: 'Codex CLI',     mech: 'skills', profile: 'codex',          detect: 'command:codex' },
  { id: 'cursor',   label: 'Cursor',        mech: 'skills', profile: 'cursor',         detect: 'command:cursor||macapp:Cursor' },
  { id: 'windsurf', label: 'Windsurf',      mech: 'skills', profile: 'windsurf',       detect: 'command:windsurf||macapp:Windsurf' },
  { id: 'cline',    label: 'Cline',         mech: 'skills', profile: 'cline',          detect: 'vscode-ext:cline' },
  { id: 'continue', label: 'Continue',      mech: 'skills', profile: 'continue',       detect: 'vscode-ext:continue' },
  { id: 'kilo',     label: 'Kilo Code',     mech: 'skills', profile: 'kilo',           detect: 'vscode-ext:kilocode' },
  { id: 'roo',      label: 'Roo Code',      mech: 'skills', profile: 'roo',            detect: 'vscode-ext:roo||cursor-ext:roo' },
  { id: 'augment',  label: 'Augment Code',  mech: 'skills', profile: 'augment',        detect: 'vscode-ext:augment' },
  { id: 'copilot',  label: 'GitHub Copilot',mech: 'skills', profile: 'github-copilot', detect: 'vscode-ext:github.copilot||vscode-ext:github.copilot-chat' },
  { id: 'aider',    label: 'Aider',         mech: 'skills', profile: 'aider-desk',     detect: 'command:aider' },
  { id: 'amp',      label: 'Sourcegraph Amp', mech: 'skills', profile: 'amp',          detect: 'command:amp' },
  { id: 'crush',    label: 'Crush',         mech: 'skills', profile: 'crush',          detect: 'command:crush' },
  { id: 'goose',    label: 'Block Goose',   mech: 'skills', profile: 'goose',          detect: 'command:goose' },
  { id: 'qwen',     label: 'Qwen Code',     mech: 'skills', profile: 'qwen-code',      detect: 'command:qwen' },
  { id: 'iflow',    label: 'iFlow CLI',     mech: 'skills', profile: 'iflow-cli',      detect: 'command:iflow' },
  { id: 'mistral',  label: 'Mistral Vibe',  mech: 'skills', profile: 'mistral-vibe',   detect: 'command:mistral' },
  { id: 'openhands',label: 'OpenHands',     mech: 'skills', profile: 'openhands',      detect: 'command:openhands' },
  { id: 'warp',     label: 'Warp',          mech: 'skills', profile: 'warp',           detect: 'command:warp' },
  { id: 'trae',     label: 'Trae',          mech: 'skills', profile: 'trae',           detect: 'command:trae' },
];

// -------------------------------------------------------------------------
// Detection
// -------------------------------------------------------------------------
const IS_WIN = process.platform === 'win32';

function expandHome(p) {
  return p.replace(/^\$HOME/, os.homedir()).replace(/^~/, os.homedir());
}

function hasCmd(cmd) {
  try {
    if (IS_WIN) return child_process.spawnSync('where', [cmd], { stdio: 'ignore' }).status === 0;
    return child_process.spawnSync('sh', ['-c', `command -v '${cmd.replace(/'/g, "'\\''")}'`], { stdio: 'ignore' }).status === 0;
  } catch (_) { return false; }
}

function macAppPresent(name) {
  if (process.platform !== 'darwin') return false;
  return [`/Applications/${name}.app`, path.join(os.homedir(), 'Applications', `${name}.app`)]
    .some(p => fs.existsSync(p));
}

function extPresent(roots, needle) {
  const re = new RegExp(needle, 'i');
  for (const r of roots) {
    if (!fs.existsSync(r)) continue;
    try { if (fs.readdirSync(r).some(e => re.test(e))) return true; } catch (_) {}
  }
  return false;
}

function vscodeExtPresent(needle) {
  const home = os.homedir();
  return extPresent([
    path.join(home, '.vscode/extensions'),
    path.join(home, '.vscode-server/extensions'),
    path.join(home, '.cursor/extensions'),
    path.join(home, '.windsurf/extensions'),
  ], needle);
}

function cursorExtPresent(needle) {
  return extPresent([path.join(os.homedir(), '.cursor/extensions')], needle);
}

function safeStat(p, method) {
  try { return fs.statSync(p)[method](); } catch (_) { return false; }
}

function detectMatch(spec) {
  if (!spec) return false;
  for (const clause of spec.split('||')) {
    const c = clause.trim();
    if (!c) continue;
    const colon = c.indexOf(':');
    const kind = colon === -1 ? c : c.slice(0, colon);
    const val = colon === -1 ? '' : expandHome(c.slice(colon + 1));
    let ok = false;
    switch (kind) {
      case 'command':    ok = hasCmd(val); break;
      case 'dir':        ok = safeStat(val, 'isDirectory'); break;
      case 'file':       ok = safeStat(val, 'isFile'); break;
      case 'macapp':     ok = macAppPresent(val); break;
      case 'vscode-ext': ok = vscodeExtPresent(val); break;
      case 'cursor-ext': ok = cursorExtPresent(val); break;
    }
    if (ok) return true;
  }
  return false;
}

// repoRoot is one level up from bin/. Present when run from a clone or the
// published npm package (which ships skills/ and commands/).
function repoRoot() {
  const root = path.resolve(__dirname, '..');
  if (fs.existsSync(path.join(root, 'skills', 'turo', 'SKILL.md'))) return root;
  return null;
}

// -------------------------------------------------------------------------
// Spawning
// -------------------------------------------------------------------------
function runSpawn(cmd, args, dry) {
  if (dry) { log(`  would run: ${cmd} ${args.join(' ')}`); return { status: 0 }; }
  log(`  $ ${cmd} ${args.join(' ')}`);
  if (IS_WIN) {
    const quoted = args.map(a => (/[\s"]/.test(a) ? `"${a.replace(/"/g, '\\"')}"` : a)).join(' ');
    return child_process.spawnSync(`${cmd} ${quoted}`, [], { shell: true, stdio: 'inherit' });
  }
  return child_process.spawnSync(cmd, args, { stdio: 'inherit' });
}

function captureSpawn(cmd, args) {
  try { return child_process.spawnSync(cmd, args, { encoding: 'utf8', timeout: 15000 }); }
  catch (_) { return { status: 1, stdout: '', stderr: '' }; }
}

function spawnOk(r) { return r && !r.error && (r.status === 0 || r.status == null); }

function copyFile(src, dest) {
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.copyFileSync(src, dest);
}

// -------------------------------------------------------------------------
// Binary install
// -------------------------------------------------------------------------
function binaryPlatform() {
  const map = {
    'darwin-x64': 'darwin_amd64', 'darwin-arm64': 'darwin_arm64',
    'linux-x64': 'linux_amd64', 'linux-arm64': 'linux_arm64',
  };
  const key = `${os.platform()}-${os.arch()}`;
  return map[key] || null;
}

function installBinary(ctx) {
  const { opts, results } = ctx;
  if (hasCmd('turo') && !opts.force) {
    log('  turo binary already on PATH (use --force to reinstall)');
    results.skipped.push(['binary', 'already on PATH']);
    return;
  }
  const platform = binaryPlatform();
  if (!platform) {
    warn(`  binary: unsupported platform ${os.platform()}-${os.arch()} — install manually from ${RELEASE_URL}/releases`);
    results.failed.push(['binary', 'unsupported platform']);
    return;
  }
  const installDir = process.env.TURO_INSTALL_DIR || path.join(os.homedir(), '.turo', 'bin');
  const binaryPath = path.join(installDir, IS_WIN ? 'turo.exe' : 'turo');
  const url = `${RELEASE_URL}/releases/download/v${VERSION}/turo_${VERSION}_${platform}.tar.gz`;

  if (opts.dryRun) {
    log(`  would download ${url}`);
    log(`  would extract to ${installDir}`);
    results.installed.push('binary');
    return;
  }

  try {
    fs.mkdirSync(installDir, { recursive: true });
    const tarPath = path.join(installDir, 'turo.tar.gz');
    const dl = child_process.spawnSync('curl', ['-fsSL', url, '-o', tarPath], { stdio: 'inherit' });
    if (!spawnOk(dl)) throw new Error('download failed');
    const ex = child_process.spawnSync('tar', ['-xzf', tarPath, '-C', installDir], { stdio: 'inherit' });
    if (!spawnOk(ex)) throw new Error('extract failed');
    fs.unlinkSync(tarPath);
    fs.chmodSync(binaryPath, 0o755);
    log(`  installed: ${binaryPath}`);
    results.installed.push('binary');
    if (!hasCmd('turo')) {
      note(`  add to PATH: export PATH="${installDir}:$PATH"`);
    }
  } catch (err) {
    warn(`  binary: ${err.message} — install manually from ${RELEASE_URL}/releases`);
    results.failed.push(['binary', err.message]);
  }
}

// -------------------------------------------------------------------------
// Agent registration
// -------------------------------------------------------------------------
function claudeConfigDir() {
  return process.env.CLAUDE_CONFIG_DIR || path.join(os.homedir(), '.claude');
}

function opencodeConfigDir() {
  if (process.env.XDG_CONFIG_HOME) return path.join(process.env.XDG_CONFIG_HOME, 'opencode');
  return path.join(os.homedir(), '.config', 'opencode');
}

// Native copy of the skill + /turo command into an agent config dir that
// reads Markdown skills and commands from disk (Claude Code, opencode).
function installNative(ctx, agent, dir, fallbackProfile) {
  const { opts, results, root } = ctx;
  say(`-> ${agent.label} detected`);
  if (!root) return skillsFallback(ctx, { id: agent.id, profile: fallbackProfile });

  const skillDest = path.join(dir, 'skills', 'turo', 'SKILL.md');
  const cmdDest = path.join(dir, 'commands', 'turo.md');
  if (opts.dryRun) {
    log(`  would copy skill   -> ${skillDest}`);
    log(`  would copy command -> ${cmdDest}`);
    results.installed.push(agent.id);
    return;
  }
  try {
    copyFile(path.join(root, 'skills', 'turo', 'SKILL.md'), skillDest);
    copyFile(path.join(root, 'commands', 'turo.md'), cmdDest);
    log(`  installed skill + /turo command into ${dir}`);
    results.installed.push(agent.id);
  } catch (err) {
    results.failed.push([agent.id, err.message]);
  }
}

function installGemini(ctx, agent) {
  const { opts, results } = ctx;
  say(`-> ${agent.label} detected`);
  if (!opts.force) {
    const r = captureSpawn('gemini', ['extensions', 'list']);
    if (r.status === 0 && /turo/i.test(r.stdout || '')) {
      note('  turo extension already installed (use --force to reinstall)');
      results.skipped.push([agent.id, 'extension already installed']);
      return;
    }
  }
  const r = runSpawn('gemini', ['extensions', 'install', RELEASE_URL], opts.dryRun);
  if (spawnOk(r)) results.installed.push(agent.id);
  else results.failed.push([agent.id, 'gemini extensions install failed']);
}

function skillsFallback(ctx, agent) {
  const { opts, results } = ctx;
  const args = ['-y', 'skills', 'add', REPO, '--skill', 'turo', '-a', agent.profile, '--yes'];
  const r = runSpawn('npx', args, opts.dryRun);
  if (spawnOk(r)) results.installed.push(agent.id);
  else results.failed.push([agent.id, `npx skills add (${agent.profile}) failed`]);
}

function installViaSkills(ctx, agent) {
  say(`-> ${agent.label} detected`);
  skillsFallback(ctx, agent);
}

// -------------------------------------------------------------------------
// Uninstall
// -------------------------------------------------------------------------
function uninstall(ctx) {
  const { opts } = ctx;
  say('turo uninstall');
  const installDir = process.env.TURO_INSTALL_DIR || path.join(os.homedir(), '.turo');
  const targets = [
    installDir,
    path.join(claudeConfigDir(), 'skills', 'turo'),
    path.join(claudeConfigDir(), 'commands', 'turo.md'),
    path.join(opencodeConfigDir(), 'skills', 'turo'),
    path.join(opencodeConfigDir(), 'commands', 'turo.md'),
  ];
  for (const p of targets) {
    if (!fs.existsSync(p)) continue;
    if (opts.dryRun) { log(`  would remove ${p}`); continue; }
    fs.rmSync(p, { recursive: true, force: true });
    log(`  removed ${p}`);
  }
  note('  skills-CLI installs (Cursor/Windsurf/etc.) — remove via your agent\'s skill manager');
  note('  Gemini extension — remove with: gemini extensions uninstall turo');
}

// -------------------------------------------------------------------------
// Output helpers
// -------------------------------------------------------------------------
function log(s)  { process.stdout.write(s + '\n'); }
function say(s)  { process.stdout.write('\n' + s + '\n'); }
function note(s) { process.stdout.write(s + '\n'); }
function warn(s) { process.stderr.write(s + '\n'); }

function printList() {
  log('turo — supported agents\n');
  for (const a of AGENTS) {
    const on = detectMatch(a.detect);
    log(`  ${on ? '[x]' : '[ ]'} ${a.id.padEnd(10)} ${a.label}`);
  }
  log('\n  [x] = detected on this machine');
}

function printHelp() {
  log(`turo installer

  npx turo                install binary + register with detected agents
  npx turo --list         list every supported agent and its status
  npx turo --only <id>    register with a specific agent (repeatable)
  npx turo --all          register with every supported agent
  npx turo --no-binary    skip the binary download, register agents only
  npx turo --force        reinstall even when already present
  npx turo --dry-run      print actions, write nothing
  npx turo --uninstall    remove the binary and registered skills
  npx turo --help         this message`);
}

function parseArgs(argv) {
  const opts = { dryRun: false, force: false, all: false, noBinary: false,
    list: false, uninstall: false, help: false, only: [] };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    switch (a) {
      case '--dry-run':   opts.dryRun = true; break;
      case '--force':     opts.force = true; break;
      case '--all':       opts.all = true; break;
      case '--no-binary': opts.noBinary = true; break;
      case '--list':      opts.list = true; break;
      case '--uninstall': opts.uninstall = true; break;
      case '--help': case '-h': opts.help = true; break;
      case '--only':      if (argv[i + 1]) opts.only.push(argv[++i]); break;
      default:
        if (a.startsWith('--only=')) opts.only.push(a.slice('--only='.length));
    }
  }
  return opts;
}

// -------------------------------------------------------------------------
// Main
// -------------------------------------------------------------------------
function main() {
  const opts = parseArgs(process.argv.slice(2));
  if (opts.help) { printHelp(); return; }
  if (opts.list) { printList(); return; }

  const ctx = {
    opts,
    root: repoRoot(),
    results: { installed: [], skipped: [], failed: [] },
  };

  if (opts.uninstall) { uninstall(ctx); summary(ctx); return; }

  say('turo installer');
  note(`  ${REPO}`);
  if (opts.dryRun) note('  (dry run — nothing will be written)');

  if (!opts.noBinary) {
    say('-> installing binary');
    installBinary(ctx);
  }

  const want = (id) => opts.all || opts.only.length === 0 || opts.only.includes(id);
  const explicit = (id) => opts.all || opts.only.includes(id);

  let detectedAny = false;
  for (const agent of AGENTS) {
    if (!want(agent.id)) continue;
    if (!explicit(agent.id) && !detectMatch(agent.detect)) continue;
    detectedAny = true;
    if (agent.mech === 'claude') installNative(ctx, agent, claudeConfigDir(), 'claude');
    else if (agent.mech === 'opencode') installNative(ctx, agent, opencodeConfigDir(), 'opencode');
    else if (agent.mech === 'gemini') installGemini(ctx, agent);
    else installViaSkills(ctx, agent);
  }

  if (!detectedAny && opts.only.length === 0 && !opts.all) {
    note('\n  no known agents detected. run `npx turo --list` to see all supported agents,');
    note('  or `npx turo --only <id>` to force a specific target.');
  }

  summary(ctx);
}

function summary(ctx) {
  const { installed, skipped, failed } = ctx.results;
  say('done');
  if (installed.length) { log('  installed:'); for (const a of installed) log(`    - ${a}`); }
  if (skipped.length)   { log('  skipped:');   for (const [id, why] of skipped) log(`    - ${id} — ${why}`); }
  if (failed.length)    { warn('  failed:');   for (const [id, why] of failed) warn(`    - ${id} — ${why}`); }
  if (failed.length) process.exitCode = 1;
}

main();
