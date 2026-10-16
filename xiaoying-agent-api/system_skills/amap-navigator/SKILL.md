---
name: amap-navigator
description: 高德地图 API 工具：支持路线规划（驾车/步行/骑行/公交）和周边 POI 搜索（餐厅、酒店、景点等）
language: python
script_path: amap_navigator.py
parameters:
  action:
    type: string
    description: "操作类型：route(路线规划)、poi_search(关键词搜索)、poi_around(周边搜索)、geocode(地理编码)"
    required: true
  api_key:
    type: string
    description: "高德 API 密钥（可选，优先使用配置文件）"
    required: false
  origin:
    type: string
    description: "起点地址或坐标（路线规划）"
    required: false
  destination:
    type: string
    description: "终点地址或坐标（路线规划）"
    required: false
  keywords:
    type: string
    description: "搜索关键词，如'餐厅'、'酒店'、'加油站'、'火锅'"
    required: false
  location:
    type: string
    description: "中心点坐标（周边搜索），格式：'经度，纬度'，如'106.713478,26.578343'"
    required: false
  radius:
    type: integer
    description: "搜索半径（米），默认 1000"
    required: false
  city:
    type: string
    description: "城市名称，如'贵阳'（poi_search 和 geocode 使用）"
    required: false
  type:
    type: string
    description: "路线类型：driving(驾车)、walking(步行)、bicycling(骑行)、transit(公交)"
    required: false
  address:
    type: string
    description: "地址文本（地理编码用）"
    required: false
  offset:
    type: integer
    description: "每页记录数，默认 20"
    required: false
  limit:
    type: integer
    description: "返回结果数量限制，默认 10"
    required: false
---

# amap-navigator

高德地图 API 工具：支持路线规划（驾车/步行/骑行/公交）和周边 POI 搜索（餐厅、酒店、景点等）

## Requirements
No specific requirements

## References
None

## Usage Examples

### 1. 周边搜索（推荐）
```python
amap_navigator(
    action="poi_around",
    location="106.713478,26.578343",  # 喷水池坐标
    keywords="火锅",
    radius=1000,
    limit=10
)
```

### 2. 关键词搜索
```python
amap_navigator(
    action="poi_search",
    city="贵阳市",
    keywords="喷水池 火锅",
    limit=10
)
```

### 3. 路线规划
```python
amap_navigator(
    action="route",
    origin="喷水池",
    destination="贵阳北站",
    type="driving"
)
```

### 4. 地理编码
```python
amap_navigator(
    action="geocode",
    address="贵阳市喷水池",
    city="贵阳"
)
```

## Notes
- **location 格式**：必须是"经度，纬度"格式（如"106.713478,26.578343"），不要使用地址文本
- **坐标获取**：可先用 geocode 操作将地址转换为坐标
- **API Key**：优先从配置文件读取，也可手动指定 api_key 参数
- **返回格式**：成功返回 {"status":"success", "data":[...]}，失败返回 {"status":"error", "info":"错误信息"}

## 常见错误码说明
| 错误码 | 说明 | 解决方案 |
|--------|------|----------|
| INVALID_USER_KEY | API Key 无效或已过期 | 检查配置文件中的 api_key 是否正确 |
| OVER_DIRECTION_RANGE | 起点或终点超出路线规划范围 | 可能是跨城市距离太远，建议分段规划 |
| NO_DATA | 该路线暂无数据 | 尝试其他路线类型（如驾车改公交） |
| PARAMETER_INVALID | 参数格式错误 | 检查坐标格式是否为"经度，纬度" |

## 使用技巧
1. **路线规划最佳实践**：直接使用地址文本（如"喷水池"），系统会自动转换为坐标
2. **周边搜索**：先用 geocode 获取目标地点坐标，再用 poi_around 搜索周边
3. **坐标验证**：系统会自动验证坐标格式，无效坐标会返回明确错误提示
