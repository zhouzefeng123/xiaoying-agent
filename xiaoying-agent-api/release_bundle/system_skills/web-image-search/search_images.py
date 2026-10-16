# -*- coding: utf-8 -*-
"""
# Web Image Search Skill

This skill searches for images on the web using Baidu Image Search.
The skill definition and parameters are managed by the `SKILL.md` file in the same directory.

**Note**:
- The system loads skill metadata from `SKILL.md` ONLY.
- This script implements the logic.
"""
import argparse
import json
import os
import re
import requests
import sys
import time
from urllib.parse import urlparse, quote

import random
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry

# Force UTF-8 output for JSON
sys.stdout.reconfigure(encoding='utf-8')

# User-Agent Pool for better stealth
USER_AGENTS = [
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/121.0",
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
]

# Constants
# Use AURAMATE_WD if set, otherwise fallback to relative path (for dev/local testing without backend)
SKILL_DIR = os.path.dirname(os.path.abspath(__file__))
env_wd = os.environ.get("AURAMATE_WD")
if env_wd:
    WORKSPACE_ROOT = os.path.abspath(env_wd)
else:
    WORKSPACE_ROOT = os.path.abspath(os.path.join(SKILL_DIR, "..", "..", "..", "workspace"))

WORKSPACE_IMAGES = os.path.join(WORKSPACE_ROOT, "images", "web_search")
VISUAL_ANALYSIS_SCRIPT = os.path.join(SKILL_DIR, "..", "..", "..", "backend", "system_skills", "visual-analysis", "visual_analysis.py")

# Try to find visual_analysis.py using AURAMATE_SKILLS_DIR if available
env_skills_dir = os.environ.get("AURAMATE_SKILLS_DIR")
if env_skills_dir:
    VISUAL_ANALYSIS_SCRIPT = os.path.join(env_skills_dir, "visual-analysis", "visual_analysis.py")

def download_image(url, save_dir):
    """Download an image from URL to save_dir."""
    try:
        # Ensure the save directory exists with explicit check
        if not os.path.exists(save_dir):
            print(f"Creating download directory: {save_dir}", file=sys.stderr)
            os.makedirs(save_dir, exist_ok=True)
        
        # Verify directory is writable
        if not os.access(save_dir, os.W_OK):
            print(f"Error: Directory {save_dir} is not writable", file=sys.stderr)
            return None

        parsed_url = urlparse(url)
        base_referer = f"{parsed_url.scheme}://{parsed_url.netloc}/" if parsed_url.scheme and parsed_url.netloc else "https://www.baidu.com/"
        host = (parsed_url.netloc or "").lower()

        # Enhanced Headers to mimic browser behavior and bypass basic anti-hotlinking
        user_agent = random.choice(USER_AGENTS)
        headers = {
            "User-Agent": user_agent,
            "Accept": "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
            "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
            "Referer": "https://www.baidu.com/", # Default to Baidu for domestic compatibility
            "Sec-Ch-Ua": '"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"',
            "Sec-Ch-Ua-Mobile": "?0",
            "Sec-Ch-Ua-Platform": '"Windows"',
            "Sec-Fetch-Dest": "image",
            "Sec-Fetch-Mode": "no-cors",
            "Sec-Fetch-Site": "cross-site",
        }
        
        # Check domain specific referers
        if "baidu.com" in host:
            headers["Referer"] = "https://image.baidu.com/"
        elif "douyin" in host or "tiktok" in host:
            # Douyin/TikTok images often require empty referer or specific one
            headers["Referer"] = "https://www.douyin.com/"
        elif "sina" in host or "weibo" in host:
            headers["Referer"] = "https://weibo.com/"
        else:
            headers["Referer"] = base_referer

        def build_session():
            session = requests.Session()
            retry = Retry(
                total=4,
                connect=3,
                read=3,
                status=3,
                status_forcelist=[429, 500, 502, 503, 504],
                backoff_factor=0.6,
                allowed_methods=frozenset(["GET", "HEAD"]),
                raise_on_status=False,
            )
            adapter = HTTPAdapter(max_retries=retry)
            session.mount("http://", adapter)
            session.mount("https://", adapter)
            return session

        session = build_session()

        # Retry logic with exponential backoff
        max_retries = 3
        response = None
        for attempt in range(max_retries):
            try:
                # Add random jitter to avoid burst detection
                if attempt > 0:
                    time.sleep(random.uniform(1.0, 3.0))

                response = session.get(url, stream=True, timeout=(8, 20), headers=headers, allow_redirects=True)
                
                # Handle 403 Forbidden specifically by trying without referer or changing UA
                if response.status_code == 403:
                    # Strategy 1: Remove Referer
                    headers.pop("Referer", None)
                    # Strategy 2: Change User-Agent
                    headers["User-Agent"] = random.choice(USER_AGENTS)
                    
                    time.sleep(random.uniform(0.5, 1.5))
                    response = session.get(url, stream=True, timeout=(8, 20), headers=headers, allow_redirects=True)

                # If server returns an HTML page, treat as failure to avoid empty/blocked files
                content_type = response.headers.get('Content-Type', '').lower()
                if 'text/html' in content_type:
                    raise requests.exceptions.RequestException("Received HTML instead of image content")

                response.raise_for_status()
                break # Success
            except requests.exceptions.RequestException as e:
                if attempt == max_retries - 1:
                    print(f"Download failed after {max_retries} attempts: {e}", file=sys.stderr)
                    return None

        if response is None:
            return None
        
        # Verify Content-Type
        content_type = response.headers.get('Content-Type', '').lower()
        if 'image' not in content_type and 'application/octet-stream' not in content_type:
            return None

        # Determine filename
        parsed = urlparse(url)
        filename = os.path.basename(parsed.path)
        if not filename or '.' not in filename:
            content_type = response.headers.get('Content-Type', '')
            ext = '.jpg'
            if 'png' in content_type:
                ext = '.png'
            elif 'jpeg' in content_type:
                ext = '.jpg'
            elif 'gif' in content_type:
                ext = '.gif'
            elif 'webp' in content_type:
                ext = '.webp'
            
            filename = f"image_{int(time.time())}{ext}"
            
        # Sanitize filename - keep only safe characters
        filename = "".join([c for c in filename if c.isalpha() or c.isdigit() or c in "._-"]).strip()
        if not filename:
             filename = f"image_{int(time.time())}.jpg"

        filepath = os.path.join(save_dir, filename)
        
        # Avoid overwriting
        base, ext = os.path.splitext(filename)
        counter = 1
        while os.path.exists(filepath):
            filepath = os.path.join(save_dir, f"{base}_{counter}{ext}")
            counter += 1
        
        bytes_written = 0
        response.raw.decode_content = True
        try:
            with open(filepath, 'wb') as f:
                for chunk in response.iter_content(chunk_size=8192):
                    if not chunk:
                        continue
                    f.write(chunk)
                    bytes_written += len(chunk)
                f.flush()
                os.fsync(f.fileno())
        finally:
            response.close()

        # Verify file size (skip if < 2KB, likely invalid or thumbnail error)
        file_size = bytes_written
        if file_size == 0 and os.path.exists(filepath):
            file_size = os.path.getsize(filepath)

        if file_size < 2048:
            try:
                os.remove(filepath)
            except OSError as remove_err:
                print(f"Failed to remove small file {filepath}: {remove_err}", file=sys.stderr)
            return None
                
        return filepath
    except Exception as e:
        print(f"Download error: {e}", file=sys.stderr)
        return None

def analyze_image_content(image_path, prompt="Describe this image in detail for a PPT slide."):
    """Analyze image content using visual-analysis skill."""
    import subprocess
    if not os.path.exists(VISUAL_ANALYSIS_SCRIPT):
        return "Visual analysis skill not found."
    
    env = os.environ.copy()
    env["SKILL_INPUT"] = json.dumps({
        "image_path": image_path,
        "prompt": prompt
    }, ensure_ascii=False)
    
    try:
        result = subprocess.run(
            [sys.executable, VISUAL_ANALYSIS_SCRIPT],
            env=env,
            capture_output=True,
            text=True,
            encoding='utf-8'
        )
        if result.returncode != 0:
            return f"Analysis failed: {result.stderr.strip()}"
        
        return result.stdout.strip()
    except Exception as e:
        return f"Error running analysis: {str(e)}"

def search_baidu_images(query, limit=5):
    """Search images using Baidu Image Search (unofficial API)."""
    url = "https://image.baidu.com/search/acjson"
    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
        "Referer": "https://image.baidu.com/"
    }
    
    # URL-encode the Chinese query to prevent encoding issues
    # Baidu requires both queryWord and word parameters
    params = {
        "tn": "resultjson_com",
        "logid": str(int(time.time() * 1000)),
        "ipn": "rj",
        "ct": "201326592",
        "is": "",
        "fp": "result",
        "queryWord": query,
        "cl": "2",
        "lm": "-1",
        "ie": "utf-8",
        "oe": "utf-8",
        "adpicid": "",
        "st": "-1",
        "z": "",
        "ic": "0",
        "hd": "",
        "latest": "",
        "copyright": "",
        "word": query,
        "s": "",
        "se": "",
        "tab": "",
        "width": "",
        "height": "",
        "face": "0",
        "istype": "2",
        "qc": "",
        "nc": "1",
        "fr": "",
        "expermode": "",
        "force": "",
        "pn": "0",
        "rn": str(limit),
        "gsm": "1e",
    }
    
    try:
        # Use params= which will automatically URL-encode the query
        response = requests.get(url, headers=headers, params=params, timeout=15)
        response.raise_for_status()
        
        # Set proper encoding for the response
        response.encoding = 'utf-8'
        
        # Parse JSON - handle potential Baidu JSON quirks
        try:
            # First try standard decoding
            data = response.json()
        except (json.JSONDecodeError, ValueError):
            # Fallback: aggressive cleaning
            text = response.text
            
            # DEBUG: Print the raw text snippet to see what's wrong
            # print(f"DEBUG RAW TEXT: {text[:500]}", file=sys.stderr)

            # 1. Replace \' with ' (valid in JS, invalid in JSON)
            text = text.replace("\\'", "'")
            
            # 2. Fix trailing commas
            text = re.sub(r',\s*}', '}', text)
            text = re.sub(r',\s*]', ']', text)
            
            # 3. Attempt to handle unescaped backslashes that aren't valid escapes
            # Replace single backslashes that are NOT followed by special chars with double backslashes
            # But this is hard. A simpler way is to use regex to find the "data" array.
            
            try:
                # Use strict=False to be more lenient
                data = json.loads(text, strict=False)
            except Exception as e:
                # Last resort: try to extract the "data": [...] part via regex
                # Baidu's JSON often wraps keys in nothing or single quotes in some edge cases?
                # Usually it is: "data": [ { ... } ]
                match = re.search(r'"data"\s*:\s*(\[.*?\])', text, re.DOTALL)
                if match:
                    try:
                        # Clean the extracted list string too
                        list_str = match.group(1)
                        # Remove \' again just in case
                        list_str = list_str.replace("\\'", "'")
                        data_list = json.loads(list_str, strict=False)
                        data = {"data": data_list}
                    except Exception as sub_e:
                        # If even that fails, print detailed error
                        print(f"Baidu JSON Parse Failed: {sub_e}", file=sys.stderr)
                        if hasattr(sub_e, 'pos'):
                             start = max(0, sub_e.pos - 50)
                             end = min(len(list_str), sub_e.pos + 50)
                             print(f"Error Context (List): ...{list_str[start:end]}...", file=sys.stderr)
                        return []
                else:
                    return []

        results = []
        if "data" in data and isinstance(data["data"], list):
            for item in data["data"]:
                if not item or not isinstance(item, dict):
                    continue
                    
                thumb_url = item.get("thumbURL")
                
                # Enhanced URL extraction logic for Baidu
                image_url = item.get("middleURL") or item.get("hoverURL") or thumb_url
                
                # Check replaceUrl list for high-res objURL
                if "replaceUrl" in item and isinstance(item["replaceUrl"], list) and len(item["replaceUrl"]) > 0:
                     obj_url = item["replaceUrl"][0].get("ObjURL")
                     if obj_url:
                         image_url = obj_url
                         
                if not image_url:
                    continue

                # Decode title if it's URL-encoded
                title = item.get("fromPageTitleEnc", query)
                try:
                    title = title.encode('utf-8').decode('utf-8')
                except:
                    pass

                results.append({
                    "title": title,
                    "url": image_url,
                    "thumbnail": thumb_url,
                    "source": "baidu"
                })
                if len(results) >= limit:
                    break
        return results
    except Exception as e:
        print(f"Baidu search error: {e}", file=sys.stderr)
        return []

def search_images(query, limit=5, download=False, analyze=False, analysis_prompt=None):
    """Main search function with optional download and analysis."""
    # Ensure the save directory exists with proper checks
    if download:
        # Check and create directory structure
        if not os.path.exists(WORKSPACE_ROOT):
            print(f"Creating workspace root: {WORKSPACE_ROOT}", file=sys.stderr)
            os.makedirs(WORKSPACE_ROOT, exist_ok=True)
        
        if not os.path.exists(WORKSPACE_IMAGES):
            print(f"Creating image directory: {WORKSPACE_IMAGES}", file=sys.stderr)
            os.makedirs(WORKSPACE_IMAGES, exist_ok=True)
        
        # Verify directory is writable
        if not os.access(WORKSPACE_IMAGES, os.W_OK):
            print(f"Warning: Image directory {WORKSPACE_IMAGES} is not writable", file=sys.stderr)
        else:
            print(f"Image directory ready: {WORKSPACE_IMAGES}", file=sys.stderr)
    
    # Fetch more candidates if downloading to handle failures
    # Baidu search results might contain invalid URLs or anti-hotlinking images
    # Increase the buffer multiplier from 3 to 10 to ensure we get enough valid images
    fetch_limit = limit * 10 if download else limit * 2
    # Cap fetch_limit at 60 to avoid excessive requests (Baidu default page size is usually 30-60)
    if fetch_limit > 60:
        fetch_limit = 60
        
    results = search_baidu_images(query, fetch_limit)
    
    if not results:
        print(f"Warning: No images found for '{query}' using Baidu.", file=sys.stderr)
            
    # Process Results
    final_results = []
    for item in results:
        if len(final_results) >= limit:
            break

        if download:
            # Try main URL first, then thumbnail
            local_path = download_image(item["url"], WORKSPACE_IMAGES)
            if not local_path and item.get("thumbnail"):
                local_path = download_image(item["thumbnail"], WORKSPACE_IMAGES)
            
            if local_path:
                item["local_path"] = local_path
                
                if analyze:
                    prompt = analysis_prompt if analysis_prompt else f"Describe this image of '{query}'. Is it suitable for a presentation slide?"
                    analysis = analyze_image_content(local_path, prompt)
                    item["analysis"] = analysis
            else:
                continue # Skip if download failed
                
        final_results.append(item)
        
    return final_results

if __name__ == "__main__":
    # Priority 1: Check SKILL_INPUT environment variable (from Tool Call)
    skill_input = os.environ.get("SKILL_INPUT")
    if skill_input:
        try:
            params = json.loads(skill_input)
            query = params.get("query")
            if not query:
                 print(json.dumps({"status": "error", "message": "Missing 'query' in SKILL_INPUT"}, ensure_ascii=False))
                 sys.exit(1)
            
            # Handle string/int/bool conversions loosely
            limit = params.get("limit", 5)
            try:
                limit = int(limit)
            except:
                limit = 5
                
            download = params.get("download", False)
            if isinstance(download, str):
                download = download.lower() == "true"
                
            analyze = params.get("analyze", False)
            if isinstance(analyze, str):
                analyze = analyze.lower() == "true"
                
            analysis_prompt = params.get("analysis_prompt")
            
            # Auto-enable download if analysis is requested
            if analyze and not download:
                download = True

            try:
                images = search_images(query, limit, download, analyze, analysis_prompt)
                print(json.dumps({"status": "success", "images": images}, ensure_ascii=False, indent=2))
                sys.exit(0)
            except Exception as e:
                print(json.dumps({"status": "error", "message": str(e)}, ensure_ascii=False))
                sys.exit(1)
                
        except json.JSONDecodeError:
            pass # Fallback to CLI args

    # Priority 2: CLI Arguments (argparse)
    def str2bool(v):
        if isinstance(v, bool):
            return v
        if v.lower() in ('yes', 'true', 't', 'y', '1'):
            return True
        elif v.lower() in ('no', 'false', 'f', 'n', '0'):
            return False
        else:
            raise argparse.ArgumentTypeError('Boolean value expected.')

    parser = argparse.ArgumentParser(description="Search and optionally download/analyze images.")
    parser.add_argument("--query", required=True, help="Search query")
    parser.add_argument("--limit", type=int, default=5, help="Number of images")
    parser.add_argument("--download", type=str2bool, nargs='?', const=True, default=False, help="Download images")
    parser.add_argument("--analyze", type=str2bool, nargs='?', const=True, default=False, help="Analyze images using visual model")
    parser.add_argument("--analysis_prompt", help="Custom prompt for visual analysis")
    
    # If no args provided and no SKILL_INPUT, print JSON error
    if len(sys.argv) == 1:
        print(json.dumps({"status": "error", "message": "Missing arguments. Usage: --query <str> [--limit <int>] [--download <bool>]"}, ensure_ascii=False))
        sys.exit(1)

    try:
        args = parser.parse_args()
    except SystemExit:
        # Catch argparse exit to output JSON error
        # Check if -h was passed, if so let it print help but wrapped? No, better to just error out in JSON for LLM
        if '-h' in sys.argv or '--help' in sys.argv:
             # Let help print to stderr, but we also print a hint
             pass
        else:
             print(json.dumps({"status": "error", "message": "Invalid arguments. Check types (e.g. limit must be int)."}, ensure_ascii=False))
        sys.exit(1)
    
    # Auto-enable download if analysis is requested
    if args.analyze and not args.download:
        args.download = True
    
    try:
        images = search_images(args.query, args.limit, args.download, args.analyze, args.analysis_prompt)
        print(json.dumps({"status": "success", "images": images}, ensure_ascii=False, indent=2))
    except Exception as e:
        print(json.dumps({"status": "error", "message": str(e)}, ensure_ascii=False))
