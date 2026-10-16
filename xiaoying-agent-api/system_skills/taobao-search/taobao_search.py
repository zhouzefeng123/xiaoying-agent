"""
淘宝商品搜索技能
使用 Playwright 控制浏览器访问淘宝网页进行搜索
完全参照 tencent-yuanbao-search 实现
"""

import asyncio
import os
import json
import re
import statistics
from collections import Counter
from playwright.async_api import async_playwright

# Cookie 文件路径 (保存在当前目录下)
COOKIE_FILE = os.path.join(os.path.dirname(__file__), "taobao_cookies.json")


class StabilityController:
    """合规稳定控制器：限流 + 重试退避 + 熔断 + 指标"""

    def __init__(
        self,
        min_interval_ms: int = 900,
        retry_max_attempts: int = 3,
        retry_base_delay_ms: int = 900,
        retry_max_delay_ms: int = 7000,
        circuit_fail_threshold: int = 5,
        circuit_cooldown_ms: int = 30000,
    ):
        self.min_interval_ms = max(0, int(min_interval_ms))
        self.retry_max_attempts = max(1, int(retry_max_attempts))
        self.retry_base_delay_ms = max(100, int(retry_base_delay_ms))
        self.retry_max_delay_ms = max(self.retry_base_delay_ms, int(retry_max_delay_ms))
        self.circuit_fail_threshold = max(1, int(circuit_fail_threshold))
        self.circuit_cooldown_ms = max(1000, int(circuit_cooldown_ms))

        self._lock = asyncio.Lock()
        self._last_request_monotonic = 0.0

        self._consecutive_failures = 0
        self._circuit_open_until = 0.0

        self.metrics = {
            "rate_waits": 0,
            "rate_wait_ms": 0,
            "attempts": 0,
            "success": 0,
            "failures": 0,
            "retries": 0,
            "circuit_open_rejects": 0,
            "retry_backoff_ms": 0,
        }

    def _now(self) -> float:
        return asyncio.get_running_loop().time()

    async def _rate_limit(self):
        if self.min_interval_ms <= 0:
            return
        async with self._lock:
            now = self._now()
            gap = self.min_interval_ms / 1000
            elapsed = now - self._last_request_monotonic
            if self._last_request_monotonic > 0 and elapsed < gap:
                wait_s = gap - elapsed
                self.metrics["rate_waits"] += 1
                self.metrics["rate_wait_ms"] += int(wait_s * 1000)
                await asyncio.sleep(wait_s)
            self._last_request_monotonic = self._now()

    def _is_circuit_open(self) -> bool:
        return self._now() < self._circuit_open_until

    def _open_circuit(self):
        self._circuit_open_until = self._now() + (self.circuit_cooldown_ms / 1000)

    def _record_success(self):
        self._consecutive_failures = 0
        self.metrics["success"] += 1

    def _record_failure(self):
        self._consecutive_failures += 1
        self.metrics["failures"] += 1
        if self._consecutive_failures >= self.circuit_fail_threshold:
            self._open_circuit()

    @staticmethod
    def _is_retryable_error(err: Exception) -> bool:
        txt = str(err).lower()
        retryable_signals = [
            "timeout",
            "timed out",
            "navigation",
            "net::err",
            "connection",
            "target closed",
            "frame was detached",
            "execution context was destroyed",
        ]
        return any(sig in txt for sig in retryable_signals)

    async def run(self, op_name: str, coro_factory):
        """
        包装异步操作：
        - 熔断打开时快速失败
        - 操作前限流
        - 失败后指数退避重试
        """
        if self._is_circuit_open():
            self.metrics["circuit_open_rejects"] += 1
            remain_ms = int((self._circuit_open_until - self._now()) * 1000)
            raise RuntimeError(f"[CIRCUIT_OPEN] {op_name} 暂停执行，剩余约 {max(remain_ms, 0)}ms")

        last_err = None
        for attempt in range(1, self.retry_max_attempts + 1):
            self.metrics["attempts"] += 1
            try:
                await self._rate_limit()
                result = await coro_factory()
                self._record_success()
                return result
            except Exception as e:
                last_err = e
                self._record_failure()

                can_retry = (attempt < self.retry_max_attempts) and self._is_retryable_error(e)
                if not can_retry:
                    raise

                self.metrics["retries"] += 1
                delay_ms = min(self.retry_max_delay_ms, self.retry_base_delay_ms * (2 ** (attempt - 1)))
                # 抖动 80%~120%
                jitter = 0.8 + (0.4 * (attempt / max(1, self.retry_max_attempts)))
                sleep_s = (delay_ms * jitter) / 1000
                self.metrics["retry_backoff_ms"] += int(sleep_s * 1000)
                print(f"[RETRY] {op_name} attempt={attempt}/{self.retry_max_attempts} error={e}")
                await asyncio.sleep(sleep_s)

        raise last_err



def _to_number_with_chinese_units(raw: str) -> float:
    if raw is None:
        return 0.0
    s = str(raw).strip()
    if not s:
        return 0.0
    s = s.replace(",", "")
    m = re.search(r"(\d+(?:\.\d+)?)", s)
    if not m:
        return 0.0
    v = float(m.group(1))
    if "万" in s:
        v *= 10000
    elif "亿" in s:
        v *= 100000000
    return v


def _parse_price(raw: str) -> float:
    return _to_number_with_chinese_units((raw or "").replace("¥", ""))


def _parse_sales(raw: str) -> float:
    return _to_number_with_chinese_units(raw or "")


def _parse_reviews(raw: str) -> float:
    return _to_number_with_chinese_units(raw or "")


def _parse_bins(bin_str: str):
    if not bin_str:
        return []
    out = []
    for seg in str(bin_str).split(","):
        seg = seg.strip()
        if not seg:
            continue
        try:
            a, b = seg.split("-")
            lo = float(a.strip())
            hi = float(b.strip())
            if hi > lo:
                out.append((lo, hi))
        except Exception:
            continue
    return out


def _calc_outliers_iqr(values):
    vals = sorted([float(v) for v in values if v is not None])
    if len(vals) < 4:
        return {"enabled": True, "lower": None, "upper": None}
    q1, q3 = statistics.quantiles(vals, n=4, method="inclusive")[0], statistics.quantiles(vals, n=4, method="inclusive")[2]
    iqr = q3 - q1
    lower = q1 - 1.5 * iqr
    upper = q3 + 1.5 * iqr
    return {"enabled": True, "lower": lower, "upper": upper}


def build_analysis(
    products: list,
    analysis_scope: str = "all_results",
    price_bins_str: str = "",
    sales_bins_str: str = "",
):
    items = products or []
    if analysis_scope == "detail_only":
        items = [x for x in items if x.get("detail_fetchable")]

    prices = [_parse_price(x.get("price", "")) for x in items]
    prices = [x for x in prices if x > 0]
    sales_vals = [_parse_sales(x.get("sales", "")) for x in items]
    sales_vals = [x for x in sales_vals if x > 0]
    review_vals = [_parse_reviews(x.get("reviews", "")) for x in items]
    review_vals = [x for x in review_vals if x > 0]

    default_price_bins = [(0, 10), (10, 30), (30, 50), (50, 100), (100, 999999999)]
    default_sales_bins = [(0, 100), (100, 1000), (1000, 10000), (10000, 100000), (100000, 999999999)]
    p_bins = _parse_bins(price_bins_str) or default_price_bins
    s_bins = _parse_bins(sales_bins_str) or default_sales_bins

    def bucketize(values, bins, unit=""):
        data = []
        for lo, hi in bins:
            c = sum(1 for v in values if (v >= lo and v < hi))
            label_hi = "+" if hi >= 999999999 else f"-{int(hi)}"
            label = f"{int(lo)}{label_hi}{unit}"
            data.append({"range": label, "count": c})
        return data

    shop_counter = Counter([str(x.get("shop", "")).strip() for x in items if str(x.get("shop", "")).strip()])
    shop_top = [{"shop": k, "count": v} for k, v in shop_counter.most_common(10)]

    brand_candidates = ["苹果", "华为", "小米", "荣耀", "vivo", "oppo", "iPhone", "mate", "redmi"]
    brand_counter = Counter()
    for x in items:
        t = str(x.get("title", ""))
        tl = t.lower()
        for b in brand_candidates:
            if b.lower() in tl:
                brand_counter[b] += 1
    brand_top = [{"brand": k, "count": v} for k, v in brand_counter.most_common(10)]

    stop_words = set(["适用", "手机", "手机壳", "新款", "保护套", "防摔", "苹果", "华为", "官方", "全包", "镜头", "高级感", "超薄"])
    kw_counter = Counter()
    for x in items:
        t = re.sub(r"[^\u4e00-\u9fa5A-Za-z0-9]", " ", str(x.get("title", "")))
        for w in t.split():
            if len(w) < 2 or w in stop_words:
                continue
            kw_counter[w] += 1
    keyword_top = [{"keyword": k, "count": v} for k, v in kw_counter.most_common(15)]

    analysis = {
        "scope": analysis_scope,
        "total_items": len(items),
        "summary": {
            "price": {
                "count": len(prices),
                "avg": round(sum(prices) / len(prices), 2) if prices else 0,
                "median": round(statistics.median(prices), 2) if prices else 0,
                "min": round(min(prices), 2) if prices else 0,
                "max": round(max(prices), 2) if prices else 0,
            },
            "sales": {
                "count": len(sales_vals),
                "avg": round(sum(sales_vals) / len(sales_vals), 2) if sales_vals else 0,
                "median": round(statistics.median(sales_vals), 2) if sales_vals else 0,
                "min": round(min(sales_vals), 2) if sales_vals else 0,
                "max": round(max(sales_vals), 2) if sales_vals else 0,
            },
            "reviews": {
                "count": len(review_vals),
                "avg": round(sum(review_vals) / len(review_vals), 2) if review_vals else 0,
                "median": round(statistics.median(review_vals), 2) if review_vals else 0,
                "min": round(min(review_vals), 2) if review_vals else 0,
                "max": round(max(review_vals), 2) if review_vals else 0,
            },
        },
        "price_bins": bucketize(prices, p_bins, unit="元"),
        "sales_bins": bucketize(sales_vals, s_bins, unit="销量"),
        "shop_top": shop_top,
        "brand_top": brand_top,
        "keyword_top": keyword_top,
        "price_outliers": _calc_outliers_iqr(prices),
        "sales_outliers": _calc_outliers_iqr(sales_vals),
    }
    return analysis

async def inject_login_modal(page):
    """在浏览器中注入登录提示模态框 (右下角悬浮，紧凑设计)"""
    inject_modal_js = """
    (function() {
        const existing = document.getElementById('auramate-login-modal');
        if (existing) existing.remove();
        
        const modal = document.createElement('div');
        modal.id = 'auramate-login-modal';
        modal.style.cssText = `
            position: fixed;
            bottom: 20px;
            right: 20px;
            z-index: 2147483647;
            background: linear-gradient(145deg, #ffffff, #fff8f5);
            padding: 22px 26px;
            border-radius: 14px;
            box-shadow: 0 10px 40px rgba(255,102,0,0.28);
            color: #333;
            font-family: system-ui, -apple-system, sans-serif;
            border: 1px solid #ffd9c0;
            width: 360px;
            max-width: 92vw;
            animation: slideUp 0.3s ease-out;
        `;
        
        const style = document.createElement('style');
        style.innerHTML = `
            @keyframes slideUp {
                from { transform: translateY(20px); opacity: 0; }
                to { transform: translateY(0); opacity: 1; }
            }
        `;
        document.head.appendChild(style);
        
        modal.innerHTML = `
            <div style="display: flex; align-items: center; margin-bottom: 14px; gap: 10px;">
                <span style="font-size: 26px;">🛒</span>
                <h3 style="margin: 0; font-size: 18px; font-weight: 700; color: #ff6600;">请登录淘宝</h3>
            </div>
            <p style="margin: 0 0 16px 0; font-size: 14px; color: #666; line-height: 1.6;">
                扫码登录后，点击下方按钮继续
            </p>
            <button id="auramate-continue-btn" style="
                background: linear-gradient(135deg, #ff6600 0%, #ff4400 100%);
                color: white;
                border: none;
                width: 100%;
                padding: 12px;
                font-size: 15px;
                font-weight: 600;
                border-radius: 10px;
                cursor: pointer;
                transition: all 0.2s;
            ">✅ 已完成登录</button>
        `;
        
        document.body.appendChild(modal);
        
        const btn = document.getElementById('auramate-continue-btn');
        btn.onmouseover = () => { btn.style.opacity = '0.9'; };
        btn.onmouseout = () => { btn.style.opacity = '1'; };
        btn.onclick = function() {
            const m = document.getElementById('auramate-login-modal');
            if (m) m.remove();
        };
    })();
    """
    await page.evaluate(inject_modal_js)

async def check_login_status(page) -> bool:
    """
    检查淘宝页面是否已登录
    判断依据：
    1. 是否存在登录弹窗 -> 存在则未登录
    2. 是否存在"亲，请登录"文本 -> 存在则未登录
    3. 是否存在用户信息元素 -> 存在则已登录
    """
    try:
        # 1. 检查是否存在登录弹窗
        login_modal = page.locator(".login-dialog, .login-modal, [class*='login-dialog']")
        if await login_modal.count() > 0:
            for i in range(await login_modal.count()):
                if await login_modal.nth(i).is_visible():
                    return False
        
        # 2. 检查是否存在"亲，请登录"文本
        login_text = page.locator("text='亲，请登录', text='请登录'")
        if await login_text.count() > 0:
            for i in range(await login_text.count()):
                if await login_text.nth(i).is_visible():
                    return False
        
        # 3. 检查是否存在用户信息（已登录标志）
        user_info = page.locator(".site-nav-user, .member-nick, .j_Username, [class*='username'], [class*='nickname']")
        if await user_info.count() > 0:
            for i in range(await user_info.count()):
                if await user_info.nth(i).is_visible():
                    return True
        
        # 4. 检查是否有登录按钮
        login_btn = page.locator("a:has-text('登录'), button:has-text('登录')")
        if await login_btn.count() > 0:
            for i in range(await login_btn.count()):
                btn = login_btn.nth(i)
                if await btn.is_visible():
                    text = await btn.inner_text()
                    if "登录" in text and "退出" not in text:
                        return False
        
        return True
    except Exception as e:
        print(f"[DEBUG] 登录状态检测异常: {e}")
        return False

async def search_taobao(
    query: str,
    max_results: int = 10,
    timeout: int = 120000,
    headless: bool = False,
    login_timeout: int = 300000,
    enable_detail_enrich: bool = False,
    resolve_click_count: int = 3,
    resolve_url_count: int = 2,
    sort: str = "default",
    min_interval_ms: int = 900,
    retry_max_attempts: int = 3,
    retry_base_delay_ms: int = 900,
    retry_max_delay_ms: int = 7000,
    circuit_fail_threshold: int = 5,
    circuit_cooldown_ms: int = 30000,
    anti_risk_level: str = "medium",
    anti_risk_jitter_ms: int = 600,
    anti_risk_break_seconds: int = 8,
    anti_risk_break_every: int = 12,
    analysis_scope: str = "all_results",
    analysis_output_mode: str = "both",
    analysis_output: str = None,
    output: str = None,
    results_output: str = None,
    price_bins: str = None,
    sales_bins: str = None,
) -> dict:
    """
    在淘宝网页上搜索商品
    策略：
    1. 尝试加载本地 Cookie
    2. 打开浏览器（可选有头/无头）
    3. 检查登录状态
    4. 如果未登录 -> 提示用户登录 -> 等待 -> 保存 Cookie
    5. 执行搜索
    """
    result = {
        "success": False,
        "query": query,
        "results": [],
        "error": "",
        "total": 0
    }

    stability = StabilityController(
        min_interval_ms=min_interval_ms,
        retry_max_attempts=retry_max_attempts,
        retry_base_delay_ms=retry_base_delay_ms,
        retry_max_delay_ms=retry_max_delay_ms,
        circuit_fail_threshold=circuit_fail_threshold,
        circuit_cooldown_ms=circuit_cooldown_ms,
    )
    result["stability"] = {
        "config": {
            "min_interval_ms": min_interval_ms,
            "retry_max_attempts": retry_max_attempts,
            "retry_base_delay_ms": retry_base_delay_ms,
            "retry_max_delay_ms": retry_max_delay_ms,
            "circuit_fail_threshold": circuit_fail_threshold,
            "circuit_cooldown_ms": circuit_cooldown_ms,
        },
        "metrics": {},
    }
    
    async with async_playwright() as p:
        print(f"[INFO] 启动浏览器 ({'无头' if headless else '有头'}模式)...")
        try:
            # 启动浏览器实例
            browser = await p.chromium.launch(
                headless=headless,
                args=["--window-size=1600,1000"]
            )
            
            # 创建上下文
            context = await browser.new_context(viewport=None)
            
            # 1. 尝试加载本地 Cookie
            if os.path.exists(COOKIE_FILE):
                try:
                    with open(COOKIE_FILE, 'r', encoding='utf-8') as f:
                        cookies = json.load(f)
                        await context.add_cookies(cookies)
                    print(f"[INFO] 已加载 {len(cookies)} 个 Cookie")
                except Exception as e:
                    print(f"[WARN] 加载 Cookie 失败: {e}")
            else:
                print("[INFO] 本地无 Cookie 文件，准备进行首次登录")
            
            page = await context.new_page()
            
            # 注入反反爬脚本
            stealth_script = """
            (function() {
                try { Object.defineProperty(navigator, 'webdriver', { get: () => undefined }); } catch(e) {}
                try { if (!window.chrome) window.chrome = { runtime: {} }; } catch(e) {}
                try {
                    const originalQuery = window.navigator.permissions.query;
                    window.navigator.permissions.query = (parameters) => (
                        parameters.name === 'notifications' ?
                        Promise.resolve({ state: Notification.permission }) :
                        originalQuery(parameters)
                    );
                } catch(e) {}
                try {
                    if (navigator.plugins.length === 0) {
                        Object.defineProperty(navigator, 'plugins', { get: () => [1, 2, 3, 4, 5] });
                    }
                } catch(e) {}
                try {
                    if (!navigator.languages || navigator.languages.length === 0) {
                        Object.defineProperty(navigator, 'languages', { get: () => ['zh-CN', 'zh'] });
                    }
                } catch(e) {}
            })();
            """
            await page.add_init_script(stealth_script)
            
            print("[INFO] 正在访问淘宝首页...")
            try:
                await stability.run(
                    "goto_taobao_home",
                    lambda: page.goto("https://www.taobao.com", timeout=timeout, wait_until="domcontentloaded")
                )
            except Exception as e:
                result["error"] = f"页面加载失败: {e}"
                await browser.close()
                return result
            
            await page.wait_for_timeout(3000)
            
            # 2. 检查登录状态
            is_logged_in = await check_login_status(page)
            
            if not is_logged_in:
                print("[WARN] 未检测到登录状态，尝试自动点击登录...")
                
                # 尝试点击登录按钮
                try:
                    # 淘宝首页的登录按钮选择器
                    login_selectors = [
                        "a.h[href*='login']",
                        "a:has-text('亲，请登录')",
                        "a:has-text('登录')",
                        ".site-nav-login a",
                        ".site-nav-sign a",
                        "a[href*='login.taobao.com']"
                    ]
                    
                    clicked = False
                    for selector in login_selectors:
                        try:
                            login_btn = page.locator(selector)
                            if await login_btn.count() > 0:
                                await login_btn.first.click()
                                print(f"[INFO] 已点击登录按钮: {selector}")
                                clicked = True
                                await page.wait_for_timeout(2000)
                                break
                        except:
                            continue
                    
                    if not clicked:
                        print("[WARN] 未找到登录按钮，请手动点击登录")
                except Exception as e:
                    print(f"[WARN] 自动点击登录失败: {e}")
                
                # 注入登录提示 (悬浮窗)
                await inject_login_modal(page)
                
                # 等待用户点击"我已完成登录"按钮
                print("[WAIT] 请在浏览器中扫码登录淘宝，完成后点击左上角的'我已完成登录'...")
                try:
                    # 等待模态框被用户关闭 (state='detached')
                    await page.wait_for_selector('#auramate-login-modal', state='detached', timeout=login_timeout)
                    
                    print("[INFO] 用户已确认登录，正在刷新页面以验证状态...")
                    # 刷新页面以确保 UI 更新
                    await stability.run(
                        "reload_after_login",
                        lambda: page.reload(wait_until="domcontentloaded")
                    )
                    await page.wait_for_timeout(5000)
                    
                    # 再次确认是否真的登录成功
                    if not await check_login_status(page):
                        result["error"] = "用户点击了完成，但似乎仍未检测到登录状态"
                        await browser.close()
                        return result
                    
                    # 登录成功，保存 Cookie
                    print("[INFO] 登录成功，正在保存 Cookie...")
                    cookies = await context.cookies()
                    with open(COOKIE_FILE, 'w', encoding='utf-8') as f:
                        json.dump(cookies, f, ensure_ascii=False, indent=2)
                    print(f"[SAVE] 已保存 {len(cookies)} 个 Cookie 到 {COOKIE_FILE}")
                        
                except Exception as e:
                    result["error"] = f"登录超时或中断: {str(e)}"
                    await browser.close()
                    return result
            
            print("[OK] 登录确认，执行搜索...")
            
            # 更新 Cookie
            try:
                cookies = await context.cookies()
                with open(COOKIE_FILE, 'w', encoding='utf-8') as f:
                    json.dump(cookies, f, ensure_ascii=False, indent=2)
            except:
                pass
            
            # 执行搜索
            await perform_search(
                page,
                query,
                max_results,
                result,
                stability,
                enable_detail_enrich=enable_detail_enrich,
                resolve_click_count=resolve_click_count,
                resolve_url_count=resolve_url_count,
                sort=sort,
            )

        except Exception as e:
            result["error"] = f"执行出错: {str(e)}"
        finally:
            result["stability"]["metrics"] = dict(stability.metrics)
            try:
                print(f"[STABILITY] {json.dumps(result['stability']['metrics'], ensure_ascii=False)}")
            except Exception:
                pass
            if 'browser' in locals():
                await browser.close()
    
    return result

def _is_detail_url(url: str) -> bool:
    if not url:
        return False
    return ("item.taobao.com/item.htm?id=" in url) or ("detail.tmall.com/item.htm?id=" in url)


def _link_source(url: str) -> str:
    u = (url or "").lower()
    if _is_detail_url(u):
        return "detail"
    if "click.simba.taobao.com" in u or "simba.taobao.com" in u:
        return "ad"
    if "store.taobao.com" in u or "shop" in u and "taobao.com" in u:
        return "store"
    if "amos.alicdn.com" in u or "getcid.aw" in u:
        return "chat"
    if u.startswith("http"):
        return "other"
    return "empty"


def sanitize_product_links(products: list) -> list:
    """统一规范链接并标注来源，避免将店铺/客服链接当作商品详情"""
    out = []
    for p in products or []:
        x = dict(p)
        raw = (x.get("product_url") or x.get("link") or "").strip()
        normalized = _normalize_product_url_py(raw)
        src = _link_source(normalized)

        # 非详情链接不作为可抓详情链接
        x["link"] = normalized
        x["product_url"] = normalized
        x["link_source"] = src
        x["detail_fetchable"] = _is_detail_url(normalized)

        # 若是明显店铺/旺旺链接，清空 product_url 以免后续误判
        if src in ("store", "chat") and not x["detail_fetchable"]:
            x["product_url"] = ""

        out.append(x)
    return out


def prioritize_products_for_output(products: list, max_results: int) -> list:
    """优先输出可跳转详情链接，店铺/客服链接降级到后面"""
    if not products:
        return []

    def rank(x: dict):
        src = str(x.get("link_source", ""))
        # 优先级：detail > ad > other > store > chat > empty
        pri = {
            "detail": 0,
            "ad": 1,
            "other": 2,
            "store": 3,
            "chat": 4,
            "empty": 5,
        }.get(src, 6)

        # 二级排序：价格高的靠前（仅用于同优先级内稳定输出）
        price = _parse_price(x.get("price", ""))
        return (pri, -price)

    ordered = sorted([dict(i) for i in products], key=rank)
    return ordered[:max_results]


def summarize_link_quality(products: list) -> dict:
    c = Counter([str(x.get("link_source", "empty")) for x in (products or [])])
    total = len(products or [])
    detail_count = sum(1 for x in (products or []) if x.get("detail_fetchable"))
    return {
        "total": total,
        "detail_fetchable": detail_count,
        "detail_fetchable_rate": round(detail_count / total, 4) if total else 0,
        "by_source": dict(c),
    }


def _normalize_product_url_py(raw: str) -> str:
    """Python 侧链接规范化（用于点击跳转后的最终 URL）"""
    if not raw:
        return ""

    from urllib.parse import urlparse, parse_qs, unquote

    def to_item(i: str) -> str:
        return f"https://item.taobao.com/item.htm?id={i}"

    def to_tmall(i: str) -> str:
        return f"https://detail.tmall.com/item.htm?id={i}"

    def _inner(v: str, depth: int = 0) -> str:
        if not v or depth > 4:
            return ""
        try:
            if v.startswith("//"):
                v = "https:" + v
            p = urlparse(v)
            host = (p.netloc or "").lower()
            qs = parse_qs(p.query or "")

            # 直链
            if "item.taobao.com" in host and qs.get("id"):
                return to_item(qs["id"][0])
            if "detail.tmall.com" in host and qs.get("id"):
                return to_tmall(qs["id"][0])

            # 跳转参数递归
            for k in ["tu", "url", "redirect", "target", "targetUrl", "redirectUrl"]:
                vals = qs.get(k) or []
                for vv in vals:
                    cur = vv
                    for _ in range(3):
                        try:
                            cur = unquote(cur)
                        except Exception:
                            pass
                        nested = _inner(cur, depth + 1)
                        if nested:
                            return nested

            # 提取可能的 itemId 参数
            for k in ["id", "itemId", "item_id", "auctionId", "nid"]:
                vals = qs.get(k) or []
                if vals and vals[0].isdigit() and 8 <= len(vals[0]) <= 20:
                    return to_tmall(vals[0]) if "tmall" in host else to_item(vals[0])

            return v if p.scheme in ("http", "https") else ""
        except Exception:
            return ""

    return _inner(raw)


async def recover_detail_urls_from_page(page, products: list) -> list:
    """从当前搜索页直接扫描详情页链接，并按标题模糊匹配回填"""
    if not products:
        return products

    try:
        candidates = await page.evaluate('''() => {
            const normalize = (raw) => {
                try {
                    if (!raw) return '';
                    const u = new URL(raw, location.href);
                    const id = u.searchParams.get('id');
                    if (!id) return '';
                    if (u.hostname.includes('detail.tmall.com')) return `https://detail.tmall.com/item.htm?id=${id}`;
                    if (u.hostname.includes('item.taobao.com')) return `https://item.taobao.com/item.htm?id=${id}`;
                    return '';
                } catch (e) { return ''; }
            };

            const out = [];
            const seen = new Set();
            const anchors = Array.from(document.querySelectorAll('a[href*="item.htm?id="]'));
            for (const a of anchors) {
                const href = normalize(a.href || '');
                if (!href || seen.has(href)) continue;
                seen.add(href);

                const t = ((a.textContent || '') + ' ' + ((a.closest('div,li,section,article')?.textContent) || '')).replace(/\s+/g, ' ').trim();
                out.push({ href, text: t.slice(0, 300) });
            }
            return out.slice(0, 500);
        }''')
    except Exception:
        candidates = []

    if not candidates:
        return products

    def score(title: str, text: str) -> int:
        if not title or not text:
            return 0
        title_tokens = [t for t in title.replace('【', ' ').replace('】', ' ').replace('/', ' ').split() if len(t) >= 2]
        if not title_tokens:
            return 0
        s = 0
        low = text.lower()
        for tk in title_tokens[:12]:
            if tk.lower() in low:
                s += min(5, len(tk))
        return s

    updated = []
    for p in products:
        item = dict(p)
        cur = (item.get("product_url") or item.get("link") or "").strip()
        if _is_detail_url(cur):
            item["detail_fetchable"] = True
            item["link_source"] = item.get("link_source") or "detail"
            updated.append(item)
            continue

        best = None
        best_score = 0
        title = item.get("title", "")
        for c in candidates:
            sc = score(title, c.get("text", ""))
            if sc > best_score:
                best_score = sc
                best = c

        if best and best_score >= 6:
            item["link"] = best["href"]
            item["product_url"] = best["href"]
            item["detail_fetchable"] = True
        else:
            item["detail_fetchable"] = False

        updated.append(item)

    return updated


async def resolve_product_urls_by_click(page, products: list, stability: StabilityController, max_resolve: int = 3) -> list:
    """在搜索页直接点击商品卡片，捕获新标签页真实落地 URL"""
    if not products:
        return products

    resolved = 0
    out = []
    card_selector = '[class*="doubleCard--"]'

    for idx, product in enumerate(products):
        p = dict(product)
        cur = (p.get("product_url") or p.get("link") or "").strip()
        if _is_detail_url(cur):
            p["detail_fetchable"] = True
            out.append(p)
            continue

        if resolved >= max_resolve:
            p["detail_fetchable"] = _is_detail_url(cur)
            out.append(p)
            continue

        try:
            card = page.locator(card_selector).nth(idx)
            if await card.count() == 0:
                p["detail_fetchable"] = False
                out.append(p)
                continue

            link = card.locator('a').first
            if await link.count() == 0:
                p["detail_fetchable"] = False
                out.append(p)
                continue

            print(f"[RESOLVE_CLICK] ({idx+1}/{len(products)}) 点击卡片捕获落地链接...")
            popup = None
            try:
                async with page.expect_popup(timeout=8000) as popinfo:
                    await link.click(button="middle", timeout=5000)
                popup = await popinfo.value
            except Exception:
                # 某些情况下不会新开页，尝试普通点击后回退
                before = page.url
                try:
                    await stability.run(
                        f"resolve_click_inline_click_{idx}",
                        lambda: link.click(timeout=5000)
                    )
                    await page.wait_for_timeout(2000)
                    now = page.url
                    if now != before:
                        normalized = _normalize_product_url_py(now)
                        if _is_detail_url(normalized):
                            p["link"] = normalized
                            p["product_url"] = normalized
                            p["detail_fetchable"] = True
                            resolved += 1
                        await stability.run(
                            f"resolve_click_go_back_{idx}",
                            lambda: page.go_back(timeout=15000)
                        )
                        await page.wait_for_timeout(1200)
                        out.append(p)
                        continue
                except Exception:
                    pass

            if popup:
                try:
                    await popup.wait_for_load_state("domcontentloaded", timeout=15000)
                except Exception:
                    pass

                # 给跳转链足够时间落到最终详情页
                final_url = popup.url
                for _ in range(8):
                    await popup.wait_for_timeout(1000)
                    cur = popup.url
                    if cur and cur != final_url:
                        final_url = cur
                    if _is_detail_url(_normalize_product_url_py(final_url)):
                        break

                normalized = _normalize_product_url_py(final_url)
                if _is_detail_url(normalized):
                    p["link"] = normalized
                    p["product_url"] = normalized
                    p["detail_fetchable"] = True
                    resolved += 1
                else:
                    # 兜底：读取页面HTML再提 id
                    try:
                        html = await popup.content()
                    except Exception:
                        html = ""
                    import re
                    m = re.search(r'(?:itemId|item_id|auctionId|id)["\'\s:=]+(\d{8,20})', html)
                    if m:
                        item_id = m.group(1)
                        p["link"] = f"https://item.taobao.com/item.htm?id={item_id}"
                        p["product_url"] = p["link"]
                        p["detail_fetchable"] = True
                        resolved += 1
                    else:
                        p["detail_fetchable"] = False
                try:
                    await popup.close()
                except Exception:
                    pass
            else:
                p["detail_fetchable"] = False

        except Exception as e:
            print(f"[WARN] 点击解析失败: {e}")
            p["detail_fetchable"] = False

        out.append(p)

    return out


async def resolve_product_urls(page, products: list, stability: StabilityController, max_resolve: int = 3) -> list:
    """通过打开可疑跳转链接获取最终落地 URL，提升详情页命中率"""
    if not products:
        return products

    resolved_count = 0
    output = []
    for idx, product in enumerate(products, 1):
        p = dict(product)
        url = (p.get("product_url") or p.get("link") or "").strip()

        if _is_detail_url(url):
            p["detail_fetchable"] = True
            output.append(p)
            continue

        need_resolve = (
            resolved_count < max_resolve and
            url.startswith("http") and
            ("click.simba.taobao.com" in url or "simba.taobao.com" in url or "ad" in url)
        )

        if not need_resolve:
            p["detail_fetchable"] = _is_detail_url(url)
            output.append(p)
            continue

        resolved_count += 1
        try:
            print(f"[RESOLVE] ({idx}/{len(products)}) 尝试解析跳转链接...")
            tmp = await page.context.new_page()
            try:
                await stability.run(
                    f"resolve_url_tmp_goto_{idx}",
                    lambda: tmp.goto(url, timeout=45000, wait_until="domcontentloaded")
                )
                await tmp.wait_for_timeout(1500)
                final_url = tmp.url
                normalized = _normalize_product_url_py(final_url)
                if _is_detail_url(normalized):
                    p["link"] = normalized
                    p["product_url"] = normalized
                    p["detail_fetchable"] = True
                else:
                    p["detail_fetchable"] = False
            finally:
                await tmp.close()
        except Exception as e:
            print(f"[WARN] 解析跳转链接失败: {e}")
            p["detail_fetchable"] = False

        output.append(p)

    return output


async def perform_search(
    page,
    query: str,
    max_results: int,
    result_dict: dict,
    stability: StabilityController,
    enable_detail_enrich: bool = True,
    resolve_click_count: int = 3,
    resolve_url_count: int = 2,
    sort: str = "default",
):
    """执行具体的搜索操作"""
    try:
        import urllib.parse
        encoded_query = urllib.parse.quote(query)
        search_url = f"https://s.taobao.com/search?q={encoded_query}"
        
        print(f"[INFO] 正在搜索: {query}...")
        await stability.run(
            "goto_search_page",
            lambda: page.goto(search_url, timeout=60000, wait_until="domcontentloaded")
        )
        
        # 等待页面加载
        print("[WAIT] 等待页面内容加载...")
        await page.wait_for_timeout(10000)
        
        # 尝试关闭可能的登录弹窗
        try:
            await page.evaluate('''() => {
                const loginElements = document.querySelectorAll('.login-dialog, .login-modal, [class*="login-dialog"]');
                loginElements.forEach(el => {
                    if (el.offsetHeight > 100) {
                        el.style.display = 'none';
                        el.remove();
                    }
                });
                const masks = document.querySelectorAll('.login-mask, .modal-mask, [class*="mask"]');
                masks.forEach(m => m.style.display = 'none');
            }''')
        except:
            pass
        
        # 按页面 Tab 排序（优先用页面原生排序）
        try:
            sort = (sort or "default").lower()
            if sort in ("sales_desc", "sales"):
                sales_tab = page.get_by_role("tab", name="销量")
                if await sales_tab.count() > 0:
                    await sales_tab.first.click(timeout=5000)
                    print("[SORT] 已切换为销量排序")
                    await page.wait_for_timeout(2500)
            elif sort in ("price_asc", "price_desc"):
                price_tab = page.get_by_role("tab", name="价格")
                if await price_tab.count() > 0:
                    await price_tab.first.click(timeout=5000)
                    await page.wait_for_timeout(1800)
                    # 价格排序通常可点击一次升序、再次降序
                    if sort == "price_desc":
                        await price_tab.first.click(timeout=5000)
                        await page.wait_for_timeout(1800)
                    print(f"[SORT] 已切换为价格排序: {sort}")
        except Exception as e:
            print(f"[WARN] 页面排序切换失败: {e}")

        # 等待商品列表
        try:
            await page.wait_for_selector('.item, .srch-item, [data-id]', timeout=15000)
            print("[INFO] 检测到商品列表")
        except:
            print("[WARN] 未检测到标准商品列表，尝试继续提取...")
        
        # 提取商品信息
        products = await extract_products(page, max_results)

        # 首先规范/清洗链接，避免店铺/旺旺链接污染
        if products:
            products = sanitize_product_links(products)

        # 先尝试从当前页面扫描真实详情页链接并回填
        if products:
            products = await recover_detail_urls_from_page(page, products)
            products = sanitize_product_links(products)

        if enable_detail_enrich:
            # 再尝试通过点击卡片捕获真实落地详情页（更可靠）
            if products and resolve_click_count > 0:
                products = await resolve_product_urls_by_click(page, products, stability, max_resolve=resolve_click_count)
                products = sanitize_product_links(products)

            # 最后尝试解析广告跳转链接为详情页（补充）
            if products and resolve_url_count > 0:
                products = await resolve_product_urls(page, products, stability, max_resolve=resolve_url_count)
                products = sanitize_product_links(products)

            # 二次进入详情页补全销量/评价
            if products:
                products = await enrich_products_from_detail(page, products, stability)
        
        # 输出前做一轮“可跳转优先”重排
        products = prioritize_products_for_output(products, max_results)

        result_dict["success"] = True
        result_dict["results"] = products
        result_dict["total"] = len(products)
        result_dict["link_quality_stats"] = summarize_link_quality(products)
        
        if products:
            print(f"[OK] 成功提取 {len(products)} 个商品")
            try:
                s = result_dict.get("link_quality_stats", {})
                print(f"[LINK] detail_fetchable={s.get('detail_fetchable', 0)}/{s.get('total', 0)} by_source={s.get('by_source', {})}")
            except Exception:
                pass
        else:
            print("[WARN] 未提取到商品，可能是页面结构变化")
            
    except Exception as e:
        result_dict["error"] = f"搜索过程出错: {e}"

async def enrich_products_from_detail(page, products: list, stability: StabilityController) -> list:
    """进入商品详情页补全销量/评价信息"""
    enriched = []

    for idx, product in enumerate(products, 1):
        p = dict(product)
        url = (p.get("product_url") or p.get("link") or "").strip()

        # 只处理淘宝/天猫详情页
        if not url or ("item.taobao.com/item.htm" not in url and "detail.tmall.com/item.htm" not in url):
            enriched.append(p)
            continue

        try:
            print(f"[DETAIL] ({idx}/{len(products)}) 抓取详情页: {url[:90]}...")
            await stability.run(
                f"detail_enrich_goto_{idx}",
                lambda: page.goto(url, timeout=45000, wait_until="domcontentloaded")
            )
            await page.wait_for_timeout(2500)

            detail = await page.evaluate('''() => {
                const text = (document.body && document.body.innerText) ? document.body.innerText : '';

                const pick = (patterns) => {
                    for (const re of patterns) {
                        const m = text.match(re);
                        if (m && m[1]) return String(m[1]).trim();
                    }
                    return '';
                };

                // 评价信息常见文案
                let reviews = pick([
                    /累计评价\s*([\d.]+[万+]?)/,
                    /([\d.]+[万+]?)\s*条评价/,
                    /([\d.]+[万+]?)\s*人评价/,
                    /([\d.]+[万+]?)\s*评价/
                ]);
                if (reviews) reviews = reviews.replace(/\s+/g, '') + '评价';

                // 销量/付款人数常见文案
                let sales = pick([
                    /([\d.]+[万+]?)\s*人付款/,
                    /月销\s*([\d.]+[万+]?)/,
                    /已售\s*([\d.]+[万+]?)/,
                    /销量\s*([\d.]+[万+]?)/
                ]);
                if (sales) {
                    if (!sales.includes('人付款') && !sales.includes('已售') && !sales.includes('月销')) {
                        sales = sales + '人付款';
                    }
                }

                return { sales, reviews };
            }''')

            if isinstance(detail, dict):
                if (not p.get("sales")) and detail.get("sales"):
                    p["sales"] = detail.get("sales")
                if (not p.get("reviews")) and detail.get("reviews"):
                    p["reviews"] = detail.get("reviews")

        except Exception as e:
            print(f"[WARN] 详情页抓取失败: {e}")

        enriched.append(p)
        await page.wait_for_timeout(800)

    return enriched

async def extract_products(page, max_results: int) -> list:
    """从页面提取商品信息"""
    await page.wait_for_timeout(2000)
    
    # 关闭可能的弹窗
    try:
        close_btns = await page.query_selector_all('button:has-text("知道了"), button:has-text("关闭"), [class*="close"], .close-btn')
        for btn in close_btns:
            try:
                await btn.click(timeout=1000)
                print("[DEBUG] 已关闭弹窗")
                break
            except:
                pass
    except:
        pass
    
    await page.wait_for_timeout(1000)
    
    # 滚动页面触发加载
    try:
        for i in range(3):
            await page.evaluate('window.scrollBy(0, 500)')
            await page.wait_for_timeout(500)
        await page.evaluate('window.scrollTo(0, 0)')
    except:
        pass
    
    products_data = await page.evaluate('''() => {
        const items = [];
        const seen = new Set();
        
        // 查找所有商品卡片
        const cards = document.querySelectorAll('[class*="doubleCard--"]');
        
        console.log(`[DEBUG] Found ${cards.length} cards`);
        
        const normalizeProductLink = (raw) => {
            try {
                if (!raw) return '';

                // 支持 //item.taobao.com/... 这种协议相对链接
                let normalizedRaw = String(raw).trim();
                if (normalizedRaw.startsWith('//')) normalizedRaw = 'https:' + normalizedRaw;

                const u = new URL(normalizedRaw, location.href);

                const toItemUrl = (id) => `https://item.taobao.com/item.htm?id=${id}`;
                const toTmallUrl = (id) => `https://detail.tmall.com/item.htm?id=${id}`;

                // 常见直达详情页
                if (u.hostname.includes('item.taobao.com') && u.searchParams.get('id')) {
                    return toItemUrl(u.searchParams.get('id'));
                }
                if (u.hostname.includes('detail.tmall.com') && u.searchParams.get('id')) {
                    return toTmallUrl(u.searchParams.get('id'));
                }

                // 广告/跳转链接里可能藏着真实链接
                const candidates = [
                    u.searchParams.get('tu'),
                    u.searchParams.get('url'),
                    u.searchParams.get('redirect'),
                    u.searchParams.get('target'),
                    u.searchParams.get('targetUrl'),
                    u.searchParams.get('redirectUrl')
                ].filter(Boolean);

                for (const c of candidates) {
                    let cur = c;
                    for (let i = 0; i < 3; i++) {
                        try { cur = decodeURIComponent(cur); } catch (e) { break; }
                        const nested = normalizeProductLink(cur);
                        if (nested) return nested;
                    }
                }

                // 从 query 参数中强提 itemId（对 click.simba 等跳转链接很关键）
                const idParamKeys = ['id', 'itemId', 'item_id', 'auctionId', 'nid'];
                for (const key of idParamKeys) {
                    const v = u.searchParams.get(key);
                    if (v && /^\d{8,20}$/.test(v)) {
                        return u.hostname.includes('tmall') ? toTmallUrl(v) : toItemUrl(v);
                    }
                }

                // 从整个 URL 文本中提取可用 itemId
                const all = u.href;
                const idPatterns = [
                    /[?&](?:id|itemId|item_id|auctionId|nid)=(\d{8,20})/i,
                    /(?:itemId|item_id|auctionId|nid)["'=:\s]+(\d{8,20})/i,
                    /\b(\d{12,20})\b/
                ];
                for (const p of idPatterns) {
                    const m = all.match(p);
                    if (m && m[1]) {
                        return u.hostname.includes('tmall') ? toTmallUrl(m[1]) : toItemUrl(m[1]);
                    }
                }

                // 从路径里直接提取 itemId（部分跳转链接）
                const pathIdMatch = u.pathname.match(/\/(?:item|detail)\.htm$/i);
                if (pathIdMatch && u.searchParams.get('id')) {
                    const id = u.searchParams.get('id');
                    if (u.hostname.includes('tmall')) return toTmallUrl(id);
                    return toItemUrl(id);
                }

                // 兜底：如果已是 http(s) 链接则保留
                if (u.protocol === 'http:' || u.protocol === 'https:') {
                    return u.href;
                }
                return '';
            } catch (e) {
                return '';
            }
        };

        cards.forEach((card) => {
            try {
                // 获取标题 - 直接从 title-- 类名获取
                let title = '';
                const titleEl = card.querySelector('[class*="title--"]');
                if (titleEl) {
                    title = titleEl.textContent.trim();
                }
                
                // 过滤无效标题
                if (!title || title.length < 3 || title.includes('点此可以') || title.includes('卖家交流')) {
                    return;
                }
                
                // 调试：打印第一个卡片的所有链接
                if (items.length === 0) {
                    const allLinks = Array.from(card.querySelectorAll('a')).map(a => a.href.substring(0, 60));
                    console.log('[DEBUG] First card links:', allLinks);
                }
                
                // 获取链接 - 提取“商品详情页链接”，并做规范化
                let link = '';

                // 方法1: 优先找详情页链接
                const itemLink = card.querySelector('a[href*="item.taobao.com/item.htm"], a[href*="detail.tmall.com/item.htm"]');
                if (itemLink) {
                    link = normalizeProductLink(itemLink.href);
                }

                // 方法2: 从任意 a 标签中筛选可用商品链接
                if (!link) {
                    const anchors = Array.from(card.querySelectorAll('a[href]'));
                    for (const a of anchors) {
                        const maybe = normalizeProductLink(a.href);
                        if (maybe.includes('item.taobao.com/item.htm?id=') || maybe.includes('detail.tmall.com/item.htm?id=')) {
                            link = maybe;
                            break;
                        }
                    }
                }

                // 方法3: 查找 data-url / data-href 中的真实详情页
                if (!link) {
                    const dataUrl = card.getAttribute('data-url') || card.getAttribute('data-href');
                    link = normalizeProductLink(dataUrl || '');
                }

                // 方法4: 最后兜底，给出第一个可访问链接
                if (!link) {
                    const clickLink = card.querySelector('a[href*="click.simba"], a[href]');
                    if (clickLink) {
                        link = normalizeProductLink(clickLink.href);
                    }
                }

                // 方法5: 从卡片属性/HTML中提取 itemId，组装详情页链接
                if (!link || (!link.includes('item.taobao.com/item.htm?id=') && !link.includes('detail.tmall.com/item.htm?id='))) {
                    try {
                        const cardHtml = card.outerHTML || '';
                        const attrs = [
                            card.getAttribute('data-id'),
                            card.getAttribute('data-itemid'),
                            card.getAttribute('data-nid'),
                            card.getAttribute('nid')
                        ].filter(Boolean).map(String);

                        const idMatches = [];
                        const patterns = [
                            /(?:item_id|itemId|auctionId|nid|itemid|data-itemid|data-nid)["'\s:=]+(\d{8,20})/gi,
                            /"id"\s*:\s*"?(\d{8,20})"?/gi,
                            /[?&]id=(\d{8,20})/gi
                        ];
                        for (const ptn of patterns) {
                            let m;
                            while ((m = ptn.exec(cardHtml)) !== null) {
                                idMatches.push(m[1]);
                            }
                        }

                        const candidateIds = [...attrs, ...idMatches].map(v => (v.match(/\d{8,20}/) || [])[0]).filter(Boolean);
                        if (candidateIds.length > 0) {
                            const itemId = candidateIds[0];
                            link = `https://item.taobao.com/item.htm?id=${itemId}`;
                        }
                    } catch (e) {}
                }
                
                // 获取价格 - 从文本中提取
                let price = '';
                const cardText = card.textContent;
                const priceMatch = cardText.match(/¥([\\d.]+)/);
                if (priceMatch) {
                    price = '¥' + priceMatch[1];
                }
                
                // 获取销量 - 从文本中提取
                let sales = '';
                const salesMatch = cardText.match(/([\\d.]+[万+]?)人付款/);
                if (salesMatch) {
                    sales = salesMatch[1] + '人付款';
                }

                // 获取评价信息（列表页通常只有评价数或“人评价”）
                let reviews = '';
                const reviewPatterns = [
                    /([\\d.]+[万+]?)\\s*评价/,
                    /([\\d.]+[万+]?)\\s*人评价/,
                    /累计评价\\s*([\\d.]+[万+]?)/,
                    /评论\\s*([\\d.]+[万+]?)/
                ];
                for (const re of reviewPatterns) {
                    const m = cardText.match(re);
                    if (m) {
                        reviews = `${m[1]}评价`;
                        break;
                    }
                }
                
                // 获取店铺 - 从文本中提取
                let shop = '';
                const shopMatch = cardText.match(/回头客[\\d.]+万\\s*([^旺]+)/);
                if (shopMatch) {
                    shop = shopMatch[1].trim();
                }
                if (!shop) {
                    // 尝试其他方式
                    const shopEl = card.querySelector('[class*="shopName"], [class*="Shop"]');
                    if (shopEl) {
                        shop = shopEl.textContent.trim().substring(0, 30);
                    }
                }
                
                const key = title;
                if (seen.has(key)) return;
                seen.add(key);
                
                const detailFetchable = !!(link && (link.includes('item.taobao.com/item.htm?id=') || link.includes('detail.tmall.com/item.htm?id=')));

                items.push({
                    title: title.replace(/\\s+/g, ' ').substring(0, 100),
                    price: price,
                    sales: sales,
                    reviews: reviews,
                    shop: shop,
                    link: link,
                    product_url: link,
                    detail_fetchable: detailFetchable
                });
            } catch (e) {}
        });

        // Fallback: 如果卡片选择器失效，尝试从页面全局数据中提取
        if (items.length === 0) {
            try {
                const fromGlobal = [];
                const pushItem = (it) => {
                    try {
                        const title = String(it.title || '').trim();
                        if (!title || seen.has(title)) return;
                        seen.add(title);
                        const link = normalizeProductLink(it.link || it.detail_url || it.item_url || '');
                        const detailFetchable = !!(link && (link.includes('item.taobao.com/item.htm?id=') || link.includes('detail.tmall.com/item.htm?id=')));
                        fromGlobal.push({
                            title: title.replace(/\\s+/g, ' ').substring(0, 100),
                            price: it.price || '',
                            sales: it.sales || '',
                            reviews: it.reviews || it.comment_count || '',
                            shop: it.shop || '',
                            link,
                            product_url: link,
                            detail_fetchable: detailFetchable
                        });
                    } catch (e) {}
                };

                // 兼容旧版淘宝 g_page_config
                const auctions = window.g_page_config?.mods?.itemlist?.data?.auctions;
                if (Array.isArray(auctions)) {
                    auctions.forEach((a) => pushItem({
                        title: a.raw_title || a.title,
                        price: a.view_price ? `¥${a.view_price}` : '',
                        sales: a.view_sales || '',
                        shop: a.nick || '',
                        link: a.detail_url
                    }));
                }

                // 兼容常见预加载状态对象
                const stateCandidates = [window.__INITIAL_STATE__, window.__PRELOADED_STATE__, window.__APP_DATA__];
                for (const state of stateCandidates) {
                    if (!state) continue;
                    const json = JSON.stringify(state);
                    const re = /(https?:\\/\\/(?:item\\.taobao\\.com|detail\\.tmall\\.com)\\/item\\.htm\\?id=\\d+)/g;
                    const matches = json.match(re) || [];
                    matches.slice(0, 200).forEach((u) => pushItem({ title: u, link: u }));
                }

                // 最后兜底：扫全页 a[href]
                if (fromGlobal.length === 0) {
                    const anchors = Array.from(document.querySelectorAll('a[href]'));
                    anchors.forEach((a) => {
                        const link = normalizeProductLink(a.href);
                        if (!link) return;
                        if (!link.includes('item.taobao.com/item.htm?id=') && !link.includes('detail.tmall.com/item.htm?id=')) return;
                        const t = (a.textContent || '').trim();
                        pushItem({ title: t || link, link });
                    });
                }

                if (fromGlobal.length > 0) {
                    console.log('[DEBUG] fallback extracted items:', fromGlobal.length);
                    return fromGlobal;
                }
            } catch (e) {}
        }
        
        return items;
    }''')
    
    print(f"[DEBUG] 提取到 {len(products_data)} 个商品")
    return products_data[:max_results]

def main(
    query: str,
    max_results: int = 10,
    timeout: int = 120000,
    headless: bool = False,
    login_timeout: int = 300000,
    enable_detail_enrich: bool = True,
    resolve_click_count: int = 3,
    resolve_url_count: int = 2,
    sort: str = "default",
    analysis_scope: str = "all_results",
    price_bins: str = "",
    sales_bins: str = "",
    analysis_output_mode: str = "both",
    analysis_output: str = None,
    results_output: str = None,
    min_interval_ms: int = 900,
    retry_max_attempts: int = 3,
    retry_base_delay_ms: int = 900,
    retry_max_delay_ms: int = 7000,
    circuit_fail_threshold: int = 5,
    circuit_cooldown_ms: int = 30000,
) -> dict:
    """主函数入口"""
    result = asyncio.run(
        search_taobao(
            query,
            max_results,
            timeout=timeout,
            headless=headless,
            login_timeout=login_timeout,
            enable_detail_enrich=enable_detail_enrich,
            resolve_click_count=resolve_click_count,
            resolve_url_count=resolve_url_count,
            sort=sort,
            min_interval_ms=min_interval_ms,
            retry_max_attempts=retry_max_attempts,
            retry_base_delay_ms=retry_base_delay_ms,
            retry_max_delay_ms=retry_max_delay_ms,
            circuit_fail_threshold=circuit_fail_threshold,
            circuit_cooldown_ms=circuit_cooldown_ms,
        )
    )
    # 生成分析结果
    try:
        analysis = build_analysis(
            result.get("results", []),
            analysis_scope=analysis_scope,
            price_bins_str=price_bins,
            sales_bins_str=sales_bins,
        )

        if analysis_output_mode in ("inline", "both"):
            result["analysis"] = analysis

        if analysis_output_mode in ("separate", "both"):
            target = analysis_output or os.path.join(os.path.dirname(__file__), "analysis_output.json")
            try:
                with open(target, 'w', encoding='utf-8') as af:
                    json.dump(analysis, af, ensure_ascii=False, indent=2)
                print(f"[SAVE] 分析结果已写入: {target}")
            except Exception as e:
                print(f"[WARN] 写入分析结果失败: {e}")
    except Exception as e:
        print(f"[WARN] 生成分析结果失败: {e}")

    # 可选：完整搜索结果落盘
    if results_output:
        try:
            with open(results_output, 'w', encoding='utf-8') as rf:
                json.dump(result, rf, ensure_ascii=False, indent=2)
            print(f"[SAVE] 搜索结果已写入: {results_output}")
        except Exception as e:
            print(f"[WARN] 写入搜索结果失败: {e}")

    return result

if __name__ == "__main__":
    import argparse
    import sys
    import io

    # 强制修改标准输出编码
    if sys.stdout.encoding != 'utf-8':
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')
    
    # 设置参数解析
    parser = argparse.ArgumentParser(description="淘宝商品搜索技能")
    parser.add_argument("--query", type=str, help="搜索查询内容", required=False)
    parser.add_argument("--max-results", "--max_results", type=int, help="最大结果数", default=10)
    parser.add_argument("--timeout", type=int, help="页面加载超时毫秒，默认 120000", default=120000)
    parser.add_argument("--login-timeout", type=int, help="等待人工登录超时毫秒，默认 300000", default=300000)
    parser.add_argument("--headless", action="store_true", help="无头模式运行（不弹浏览器窗口）")
    parser.add_argument("--no-detail-enrich", action="store_true", help="关闭详情页补全（更快）")
    parser.add_argument("--resolve-click-count", type=int, help="点击卡片解析详情链接数量，默认 3", default=3)
    parser.add_argument("--resolve-url-count", type=int, help="直接解析广告链接数量，默认 2", default=2)
    parser.add_argument("--sort", type=str, choices=["default", "sales_desc", "price_asc", "price_desc"], default="default", help="排序方式：default/sales_desc/price_asc/price_desc")

    # 稳定性参数（合规降风险）
    parser.add_argument("--min-interval-ms", type=int, default=900, help="请求最小间隔毫秒，默认 900")
    parser.add_argument("--retry-max-attempts", type=int, default=3, help="最大重试次数（含首次），默认 3")
    parser.add_argument("--retry-base-delay-ms", type=int, default=900, help="重试基础退避毫秒，默认 900")
    parser.add_argument("--retry-max-delay-ms", type=int, default=7000, help="重试最大退避毫秒，默认 7000")
    parser.add_argument("--circuit-fail-threshold", type=int, default=5, help="连续失败达到阈值触发熔断，默认 5")
    parser.add_argument("--circuit-cooldown-ms", type=int, default=30000, help="熔断冷却毫秒，默认 30000")

    # 分析参数
    parser.add_argument("--analysis-scope", type=str, choices=["all_results", "detail_only"], default="all_results", help="分析范围：all_results/detail_only")
    parser.add_argument("--analysis-output-mode", type=str, choices=["inline", "separate", "both"], default="both", help="分析输出：inline/separate/both")
    parser.add_argument("--analysis-output", type=str, help="分析结果输出文件路径（separate/both 时生效）", required=False)
    parser.add_argument("--output", "--results-output", dest="results_output", type=str, help="完整搜索结果输出文件路径（JSON）", required=False)
    parser.add_argument("--price-bins", type=str, default="", help="价格区间，如 '0-10,10-30,30-50,50-999999'")
    parser.add_argument("--sales-bins", type=str, default="", help="销量区间，如 '0-100,100-1000,1000-10000,10000-999999999'")

    # 兼容位置参数
    parser.add_argument("positional_query", nargs="?", help="搜索查询内容（位置参数）")

    args = parser.parse_args()

    # 优先使用 --query，如果没有则尝试位置参数
    if args.query:
        query = args.query
    elif args.positional_query:
        query = args.positional_query
    else:
        # 尝试从 sys.argv 获取
        if len(sys.argv) > 1 and not sys.argv[1].startswith("-"):
            query = " ".join([arg for arg in sys.argv[1:] if not arg.startswith("-")])
        else:
            query = "手机"

    result = main(
        query=query,
        max_results=args.max_results,
        timeout=args.timeout,
        headless=args.headless,
        login_timeout=args.login_timeout,
        enable_detail_enrich=(not args.no_detail_enrich),
        resolve_click_count=max(0, args.resolve_click_count),
        resolve_url_count=max(0, args.resolve_url_count),
        sort=args.sort,
        analysis_scope=args.analysis_scope,
        price_bins=args.price_bins,
        sales_bins=args.sales_bins,
        analysis_output_mode=args.analysis_output_mode,
        analysis_output=args.analysis_output,
        results_output=args.results_output,
        min_interval_ms=max(0, args.min_interval_ms),
        retry_max_attempts=max(1, args.retry_max_attempts),
        retry_base_delay_ms=max(100, args.retry_base_delay_ms),
        retry_max_delay_ms=max(100, args.retry_max_delay_ms),
        circuit_fail_threshold=max(1, args.circuit_fail_threshold),
        circuit_cooldown_ms=max(1000, args.circuit_cooldown_ms),
    )
    
    # 输出摘要 + 商品列表（含链接）
    try:
        output = {
            "success": result.get("success"),
            "query": result.get("query"),
            "total": result.get("total"),
            "error": result.get("error", ""),
            "items": result.get("results", [])
        }
        # 如果指定了 results_output 路径，也保存到文件
        if args.results_output:
            with open(args.results_output, "w", encoding="utf-8") as f:
                json.dump(output, f, ensure_ascii=False, indent=2)
        print(json.dumps(output, ensure_ascii=False))
    except Exception:
        pass