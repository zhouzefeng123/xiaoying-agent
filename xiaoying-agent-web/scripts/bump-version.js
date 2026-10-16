
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const packageJsonPath = path.join(__dirname, '../package.json');

// Read JSON
const pkg = JSON.parse(fs.readFileSync(packageJsonPath, 'utf8'));

const versionParts = pkg.version.split('.').map(Number);
versionParts[2] += 1; // Increment Patch version
const newVersion = versionParts.join('.');

pkg.version = newVersion;
fs.writeFileSync(packageJsonPath, JSON.stringify(pkg, null, 2) + '\n');

console.log(`[Version] Bumped version to ${newVersion}`);
