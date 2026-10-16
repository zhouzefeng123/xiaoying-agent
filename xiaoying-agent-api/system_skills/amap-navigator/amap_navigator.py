"""
高德地图 API 工具 (v5 版本)
支持：路线规划、POI 搜索、周边搜索、地理编码
API Key 优先级：用户传入 > 配置文件 > 环境变量
"""

import os
import sys
import json
import urllib.request
import urllib.parse
from typing import Dict, Any, List, Optional
from pathlib import Path


def load_config() -> Dict[str, Any]:
    """从配置文件加载高德 API 配置"""
    config_paths = [
        # 技能目录配置（优先）
        Path(__file__).parent / "amap_config.json",
        # 工作区配置
        Path(__file__).parent.parent.parent.parent / "workspace" / "config" / "amap_config.json",
        # 当前目录配置
        Path.cwd() / "config" / "amap_config.json",
        Path.cwd() / "amap_config.json",
        # 用户目录配置
        Path.home() / ".auramate" / "amap_config.json",
    ]
    
    for config_path in config_paths:
        if config_path.exists():
            try:
                with open(config_path, 'r', encoding='utf-8') as f:
                    return json.load(f)
            except Exception:
                continue
    return {}


def get_api_key(user_provided_key: str = "") -> str:
    """
    获取 API Key，优先级：
    1. 用户传入的 key
    2. 配置文件中的 key
    3. 环境变量 AMAP_API_KEY
    """
    if user_provided_key:
        return user_provided_key
    
    config = load_config()
    config_key = config.get("api_key", "")
    if config_key:
        return config_key
    
    env_key = os.environ.get('AMAP_API_KEY', '')
    if env_key:
        return env_key
    
    return ""


def run(
    action: str = "",
    api_key: str = "",
    origin: Optional[str] = None,
    destination: Optional[str] = None,
    keywords: Optional[str] = None,
    location: Optional[str] = None,
    radius: int = 1000,
    city: Optional[str] = None,
    type: str = "driving",
    address: Optional[str] = None,
    offset: int = 20,
    limit: int = 10,
    **kwargs
) -> Dict[str, Any]:
    """
    高德地图 API 工具 (v5 版本)
    
    Args:
        action: 操作类型 (route/poi_search/poi_around/geocode)
        api_key: 高德 API 密钥（可选，优先使用配置文件）
        origin: 起点
        destination: 终点
        keywords: 搜索关键词
        location: 中心点
        radius: 搜索半径
        city: 城市
        type: 路线类型
        address: 地址（地理编码）
        offset: 每页记录数
        limit: 结果数量限制
    
    Returns:
        API 响应结果
    """
    
    # 参数验证
    if not action:
        return {"status": "error", "info": "缺少必需参数：action"}
    
    # 获取 API Key
    api_key = get_api_key(api_key)
    if not api_key:
        return {
            "status": "error", 
            "info": "缺少 API Key。请：1) 传入 api_key 参数 2) 创建 workspace/config/amap_config.json 3) 设置 AMAP_API_KEY 环境变量"
        }
    
    # 加载配置 - v5 API
    config = load_config()
    base_url = config.get("base_url", "https://restapi.amap.com/v5")
    timeout = config.get("timeout", 15)
    
    def http_get(url: str) -> Dict:
        """发送 HTTP GET 请求"""
        try:
            req = urllib.request.Request(url, headers={
                'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36'
            })
            with urllib.request.urlopen(req, timeout=timeout) as response:
                result = response.read().decode('utf-8')
                return json.loads(result)
        except Exception as e:
            return {"status": "0", "info": f"请求失败：{str(e)}"}
    
    def format_poi(poi: Dict) -> Dict:
        """格式化 POI 信息"""
        biz_ext = poi.get("biz_ext", {}) or {}
        return {
            "名称": poi.get("name", ""),
            "类型": poi.get("type", ""),
            "地址": poi.get("address", ""),
            "坐标": poi.get("location", ""),
            "距离": f"{poi.get('distance', '')}米" if poi.get('distance') else "",
            "电话": poi.get("tel", ""),
            "评分": biz_ext.get("rating", ""),
            "消费": biz_ext.get("cost", "")
        }
    
    def format_route(route: Dict, route_type: str) -> Dict:
        """格式化路线信息"""
        result = {"类型": route_type, "总距离": "", "总时间": "", "详细步骤": []}
        
        if route_type == "driving":
            path = route.get("route", {}).get("paths", [{}])[0]
            result["总距离"] = f"{path.get('distance', '0')}米"
            result["总时间"] = f"{int(path.get('duration', 0)) // 60}分钟"
            steps = []
            for step in path.get("steps", []):
                steps.append({
                    "指令": step.get("instruction", "").replace("<b>", "").replace("</b>", ""),
                    "距离": f"{step.get('distance', '0')}米",
                    "时间": f"{int(step.get('duration', 0)) // 60}分钟"
                })
            result["详细步骤"] = steps[:10]
            
        elif route_type == "walking":
            path = route.get("route", {}).get("paths", [{}])[0]
            result["总距离"] = f"{path.get('distance', '0')}米"
            result["总时间"] = f"{int(path.get('duration', 0)) // 60}分钟"
            steps = []
            for step in path.get("steps", []):
                steps.append(step.get("instruction", "").replace("<b>", "").replace("</b>", ""))
            result["详细步骤"] = steps[:10]
            
        elif route_type == "bicycling":
            path = route.get("data", {}).get("paths", [{}])[0]
            result["总距离"] = f"{path.get('distance', '0')}米"
            result["总时间"] = f"{int(path.get('duration', 0)) // 60}分钟"
            
        elif route_type == "transit":
            path = route.get("route", {}).get("transits", [{}])[0]
            result["总距离"] = f"{path.get('distance', '0')}米"
            result["总时间"] = f"{int(path.get('duration', 0)) // 60}分钟"
            result["花费"] = f"{path.get('cost', {}).get('transit_fee', '0')}元"
            steps = []
            for seg in path.get("segments", []):
                bus = seg.get("bus", {})
                if bus:
                    lines = bus.get("buslines", [{}])
                    if lines:
                        steps.append(f"乘坐 {lines[0].get('name', '公交')} ({lines[0].get('via_stops_num', 0)}站)")
                walk = seg.get("walking", {})
                if walk:
                    steps.append(f"步行 {walk.get('distance', '0')}米")
            result["详细步骤"] = steps
        
        return result
    
    # 1. 路线规划
    if action == "route":
        if not origin or not destination:
            return {"status": "error", "info": "路线规划需要起点 (origin) 和终点 (destination)"}
        
        def geocode_addr(addr: str) -> str:
            """地理编码（地址转坐标）"""
            url = f"{base_url}/geocode/geo?key={api_key}&address={urllib.parse.quote(addr)}"
            if city:
                url += f"&city={urllib.parse.quote(city)}"
            data = http_get(url)
            if data.get("status") == "1" and data.get("geocodes"):
                return data["geocodes"][0].get("location", "")
            return addr
        
        # 验证 type 参数
        valid_types = ["driving", "walking", "bicycling", "transit"]
        if type not in valid_types:
            return {"status": "error", "info": f"不支持的路线类型：{type}。可选值：{', '.join(valid_types)}"}
        
        # 转换地址为坐标
        def is_coordinate(loc: str) -> bool:
            """验证是否为有效的经纬度坐标"""
            if not loc or ',' not in loc:
                return False
            parts = loc.split(',')
            if len(parts) != 2:
                return False
            try:
                lon, lat = float(parts[0]), float(parts[1])
                return -180 <= lon <= 180 and -90 <= lat <= 90
            except ValueError:
                return False
        
        origin_loc = origin if is_coordinate(origin) else geocode_addr(origin)
        dest_loc = destination if is_coordinate(destination) else geocode_addr(destination)
        
        # 验证坐标转换结果
        if not origin_loc or ',' not in origin_loc:
            return {"status": "error", "info": f"无法解析起点坐标：{origin}，请先确认地址是否正确"}
        if not dest_loc or ',' not in dest_loc:
            return {"status": "error", "info": f"无法解析终点坐标：{destination}，请先确认地址是否正确"}
        
        route_url = ""
        if type == "driving":
            route_url = f"{base_url}/direction/driving?key={api_key}&origin={origin_loc}&destination={dest_loc}"
        elif type == "walking":
            route_url = f"{base_url}/direction/walking?key={api_key}&origin={origin_loc}&destination={dest_loc}"
        elif type == "bicycling":
            route_url = f"{base_url}/direction/bicycling?key={api_key}&origin={origin_loc}&destination={dest_loc}"
        elif type == "transit":
            route_url = f"{base_url}/direction/transit/integrated?key={api_key}&origin={origin_loc}&destination={dest_loc}&city={urllib.parse.quote(city or '北京')}"
        else:
            return {"status": "error", "info": f"不支持的路线类型：{type}"}
        
        data = http_get(route_url)
        if data.get("status") == "1":
            return {"status": "success", "data": format_route(data, type)}
        else:
            return {"status": "error", "info": data.get("info", "路线规划失败")}
    
    # 2. 关键词搜索
    elif action == "poi_search":
        if not keywords:
            return {"status": "error", "info": "POI 搜索需要关键词 (keywords)"}
        
        search_url = f"{base_url}/place/text?key={api_key}&keywords={urllib.parse.quote(keywords)}&offset={offset}"
        if city:
            search_url += f"&city={urllib.parse.quote(city)}"
        
        data = http_get(search_url)
        if data.get("status") == "1":
            pois = data.get("pois", [])
            limit_int = int(limit) if limit else 10
            pois = pois[:limit_int]
            return {"status": "success", "count": len(pois), "data": [format_poi(p) for p in pois]}
        else:
            return {"status": "error", "info": data.get("info", "搜索失败")}
    
    # 3. 周边搜索
    elif action == "poi_around":
        if not location:
            return {"status": "error", "info": "周边搜索需要中心点 (location)"}
        
        search_url = f"{base_url}/place/around?key={api_key}&location={location}&radius={radius}&offset={offset}"
        if keywords:
            search_url += f"&keywords={urllib.parse.quote(keywords)}"
        
        data = http_get(search_url)
        if data.get("status") == "1":
            pois = data.get("pois", [])
            limit_int = int(limit) if limit else 10
            pois = pois[:limit_int]
            return {"status": "success", "count": len(pois), "data": [format_poi(p) for p in pois]}
        else:
            return {"status": "error", "info": data.get("info", "周边搜索失败")}
    
    # 4. 地理编码
    elif action == "geocode":
        if not address:
            return {"status": "error", "info": "地理编码需要地址 (address)"}
        
        geo_url = f"{base_url}/geocode/geo?key={api_key}&address={urllib.parse.quote(address)}"
        if city:
            geo_url += f"&city={urllib.parse.quote(city)}"
        
        data = http_get(geo_url)
        if data.get("status") == "1":
            geocodes = data.get("geocodes", [])
            if geocodes:
                return {
                    "status": "success",
                    "data": {
                        "坐标": geocodes[0].get("location", ""),
                        "省份": geocodes[0].get("province", ""),
                        "城市": geocodes[0].get("city", ""),
                        "区县": geocodes[0].get("district", "")
                    }
                }
            return {"status": "error", "info": "未找到该地址的坐标"}
        else:
            return {"status": "error", "info": data.get("info", "地理编码失败")}
    
    else:
        return {"status": "error", "info": f"不支持的操作：{action}。支持：route, poi_search, poi_around, geocode"}


if __name__ == "__main__":
    try:
        input_str = os.environ.get("SKILL_INPUT", "{}")
        kwargs = json.loads(input_str)
        result = run(**kwargs)
        print(json.dumps(result, ensure_ascii=False))
    except Exception as e:
        print(json.dumps({"status": "error", "info": str(e)}, ensure_ascii=False))