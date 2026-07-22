#!/usr/bin/env node
// turo installer — downloads the right binary from GitHub releases.
// Runs via `npx turo` or `npm install -g turo`.

const os = require('os');
const fs = require('fs');
const path = require('path');
const { execSync } = require('child_process');

const REPO = 'kdeps/turo';
const VERSION = '0.1.0';

function getPlatform() {
  const platform = os.platform();
  const arch = os.arch();
  const map = {
    'darwin-x64': 'darwin_amd64',
    'darwin-arm64': 'darwin_arm64',
    'linux-x64': 'linux_amd64',
    'linux-arm64': 'linux_arm64',
  };
  const key = `${platform}-${arch}`;
  if (!map[key]) {
    console.error(`turo: unsupported platform ${key}`);
    process.exit(1);
  }
  return map[key];
}

async function main() {
  const platform = getPlatform();
  const installDir = path.join(os.homedir(), '.turo', 'bin');
  const binaryPath = path.join(installDir, 'turo');

  // Check if already installed at the correct version.
  if (fs.existsSync(binaryPath)) {
    try {
      const out = execSync(`"${binaryPath}" --help`, { encoding: 'utf8', timeout: 5000 });
      if (out.includes('Usage of turo')) {
        console.log(`turo ${VERSION} already installed at ${binaryPath}`);
        return;
      }
    } catch (e) { /* reinstall */ }
  }

  fs.mkdirSync(installDir, { recursive: true });

  const url = `https://github.com/${REPO}/releases/download/v${VERSION}/turo_${VERSION}_${platform}.tar.gz`;
  console.log(`Downloading turo ${VERSION} for ${platform}...`);

  const tarPath = path.join(installDir, 'turo.tar.gz');
  execSync(`curl -fsSL "${url}" -o "${tarPath}"`, { stdio: 'inherit' });
  execSync(`tar -xzf "${tarPath}" -C "${installDir}"`, { stdio: 'inherit' });
  fs.unlinkSync(tarPath);
  fs.chmodSync(binaryPath, 0o755);

  console.log(`turo ${VERSION} installed to ${binaryPath}`);

  // Suggest PATH addition.
  const pathFile = path.join(os.homedir(), '.bashrc');
  const pathLine = `export PATH="$HOME/.turo/bin:$PATH"`;
  if (fs.existsSync(pathFile)) {
    const content = fs.readFileSync(pathFile, 'utf8');
    if (!content.includes('.turo/bin')) {
      console.log(`\nAdd to PATH: echo '${pathLine}' >> ${pathFile}`);
    }
  }
}

main().catch(e => { console.error(e.message); process.exit(1); });
