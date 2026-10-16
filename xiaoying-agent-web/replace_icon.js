import { createRequire } from 'module';
const require = createRequire(import.meta.url);
const rcedit = require('rcedit');
import path from 'path';
import fs from 'fs';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const packageJson = JSON.parse(fs.readFileSync(path.resolve(__dirname, 'package.json'), 'utf-8'));

async function updateIcon() {
  const version = packageJson.version;
  const exePath = path.resolve(__dirname, `release-v2/${version}/win-unpacked/XiaoYing.exe`);
  const iconPath = path.resolve(__dirname, 'build/icon.ico');

  console.log('--- 打包后图标替换工具 ---');
  
  if (!fs.existsSync(exePath)) {
    console.error(`错误: 找不到 EXE 文件: ${exePath}`);
    console.error('请先运行 npm run package 生成 EXE。');
    process.exitCode = 1;
    return;
  }

  if (!fs.existsSync(iconPath)) {
    console.error(`错误: 找不到图标文件: ${iconPath}`);
    process.exitCode = 1;
    return;
  }

  console.log(`正在修改 EXE: ${exePath}`);
  console.log(`使用图标: ${iconPath}`);

  try {
    await rcedit(exePath, {
      icon: iconPath
    });
    console.log('✅ 图标替换成功！请检查 EXE 文件。');
  } catch (error) {
    console.error('❌ 替换失败:', error);
    process.exitCode = 1;
  }
}

updateIcon();
