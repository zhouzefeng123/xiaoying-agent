"""
@tool anti-bot-bypass
@description 生成用于绕过反爬虫检测的 JavaScript 注入脚本 (Stealth JS)。
@return script string 返回经过精心构造的 JavaScript 代码，用于掩盖浏览器的自动化特征。

# 反爬虫绕过技能 (Anti-Bot Bypass)

当 Chrome 浏览器访问网站遇到反爬虫拦截（如 Cloudflare 验证、WebDriver 检测）时，使用此技能。

## 原理
移除 `navigator.webdriver` 标记，伪造插件、语言、WebGL 指纹等。

## 使用方法
1. 调用技能: `script = anti-bot-bypass()` (无参数)
2. 注入浏览器: 使用 `chrome_evaluate(expression=script)` 将返回的代码注入到当前页面。
   * 最佳实践: 在 `navigate` 后立即注入并刷新，或者在操作前注入。
"""
import sys

# Force UTF-8 output
sys.stdout.reconfigure(encoding='utf-8')

def get_stealth_script():
    """
    Returns a JavaScript string that attempts to hide automation features.
    Based on puppeteer-extra-plugin-stealth techniques.
    """
    script = """
(function() {
    // 1. Pass the Webdriver Test
    try {
        Object.defineProperty(navigator, 'webdriver', {
            get: () => undefined
        });
    } catch (e) {}

    // 2. Mock Chrome Object
    try {
        if (!window.chrome) {
            window.chrome = {
                runtime: {}
            };
        }
    } catch (e) {}

    // 3. Pass Permissions Test
    try {
        const originalQuery = window.navigator.permissions.query;
        window.navigator.permissions.query = (parameters) => (
            parameters.name === 'notifications' ?
            Promise.resolve({ state: Notification.permission }) :
            originalQuery(parameters)
        );
    } catch (e) {}

    // 4. Mock Plugins
    try {
        if (navigator.plugins.length === 0) {
            Object.defineProperty(navigator, 'plugins', {
                get: () => [1, 2, 3, 4, 5]
            });
        }
    } catch (e) {}

    // 5. Mock Languages
    try {
        if (!navigator.languages || navigator.languages.length === 0) {
            Object.defineProperty(navigator, 'languages', {
                get: () => ['en-US', 'en']
            });
        }
    } catch (e) {}
    
    // 6. WebGL Vendor/Renderer Mock (Basic)
    try {
        const getParameter = WebGLRenderingContext.prototype.getParameter;
        WebGLRenderingContext.prototype.getParameter = function(parameter) {
            // UNMASKED_VENDOR_WEBGL
            if (parameter === 37445) {
                return 'Intel Inc.';
            }
            // UNMASKED_RENDERER_WEBGL
            if (parameter === 37446) {
                return 'Intel(R) Iris(R) Xe Graphics';
            }
            return getParameter(parameter);
        };
    } catch (e) {}

    console.log('Stealth script injected successfully.');
    return 'Stealth mode activated';
})();
"""
    return script.strip()

if __name__ == "__main__":
    try:
        js_code = get_stealth_script()
        print(js_code)
    except Exception as e:
        print(f"Error generating stealth script: {e}")
        sys.exit(1)
