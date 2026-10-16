import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const clientDir = path.join(__dirname, '..');
const packageJsonPath = path.join(clientDir, 'package.json');

const pkg = JSON.parse(fs.readFileSync(packageJsonPath, 'utf8'));
const publishConfig = Array.isArray(pkg.build?.publish) ? pkg.build.publish[0] : pkg.build?.publish;

if (!publishConfig || publishConfig.provider !== 'generic' || !publishConfig.url) {
  console.error('[app-update.yml] Missing generic publish configuration in client/package.json');
  process.exitCode = 1;
} else {
  const version = pkg.version;
  const resourcesDir = path.join(clientDir, 'release-v2', version, 'win-unpacked', 'resources');
  const outputPath = path.join(resourcesDir, 'app-update.yml');
  const normalizedUrl = String(publishConfig.url).endsWith('/')
    ? String(publishConfig.url)
    : `${String(publishConfig.url)}/`;

  if (!fs.existsSync(resourcesDir)) {
    console.error(`[app-update.yml] Resources directory not found: ${resourcesDir}`);
    process.exitCode = 1;
  } else {
    const content = [
      `provider: ${publishConfig.provider}`,
      `url: ${normalizedUrl}`,
      `updaterCacheDirName: ${pkg.name}-updater`,
      '',
    ].join('\n');

    fs.writeFileSync(outputPath, content, 'utf8');
    console.log(`[app-update.yml] Wrote ${outputPath}`);
  }
}