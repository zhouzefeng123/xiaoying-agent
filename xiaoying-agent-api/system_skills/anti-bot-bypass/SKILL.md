---
name: anti-bot-bypass
description: 生成用于绕过反爬虫检测的 JavaScript 注入脚本 (Stealth JS)。
language: python
script_path: bypass.py
parameters: {}
---

# 反爬虫绕过技能 (Anti-Bot Bypass)

当 Chrome 浏览器访问网站遇到反爬虫拦截（如 Cloudflare 验证、WebDriver 检测、滑块验证前的环境检测）时，使用此技能获取一段“隐身”脚本。

## 原理

此技能返回经过精心构造的 JavaScript 代码，用于掩盖浏览器的自动化特征（如 `navigator.webdriver`），模拟真实用户的浏览器指纹。

## 使用方法

1.  **调用技能**: `anti-bot-bypass()` (无参数)
2.  **获取脚本**: 技能将返回一段 JavaScript 代码字符串。
3.  **注入浏览器**: 使用 `chrome_evaluate(expression=SCRIPT)` 将返回的代码注入到当前页面。
    *   **最佳实践**: 在页面加载的早期（如 `Page.addScriptToEvaluateOnNewDocument`）注入效果最好。但由于 MCP 限制，通常在 `navigate` 后立即注入并刷新，或者在操作前注入。

## 示例

1.  Agent 发现被拦截。
2.  调用 `script = anti-bot-bypass()`。
3.  调用 `chrome_evaluate(expression=script)`。
4.  刷新页面或继续操作。

## 功能特性

*   移除 `navigator.webdriver` 标记。
*   伪造 `navigator.plugins` 和 `navigator.languages`。
*   伪造 `window.chrome` 对象。
*   覆盖 `navigator.permissions.query` 以通过权限检测。
*   模拟 WebGL 指纹（基础）。
