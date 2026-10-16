---
name: "web-image-search"
description: "使用百度图片搜索查找网络图片。已针对国内网络优化（自动绕过 403 Forbidden）。如果需要在文档（PPT/Word）中使用图片或分析其内容，必须设置 'download': true。"
language: "python"
script_path: "search_images.py"
parameters:
  query:
    type: "string"
    description: "搜索关键词。如 'Guiyang Jiaxiu Pavilion'。"
    required: true
  limit:
    type: "string"
    description: "最大结果数，默认 5。CLI 参数：--limit。注意：可以传入字符串或整数。"
    required: false
  download:
    type: "string"
    description: "是否下载图片到本地（默认 false）。CLI 参数：--download true。注意：可以传入字符串 'true'/'false' 或布尔值。"
    required: false
  analyze:
    type: "string"
    description: "是否分析图片内容（默认 false）。如果为 true，会返回图片描述。需要设置 download=true。"
    required: false
---

## 📖 使用示例

### ✅ 正确用法

```python
# 示例 1：下载图片到本地
web-image-search(
    query="贵阳甲秀楼",
    limit=5,
    download=true,      # 必须设置为 true 才会下载
    analyze=false
)

# 示例 2：下载并分析图片内容
web-image-search(
    query="杨幂 维密写真",
    limit=3,
    download=true,      # 必须先下载才能分析
    analyze=true        # 分析图片内容
)

# 示例 3：仅搜索，不下载（只返回 URL）
web-image-search(
    query="风景图片",
    limit=10
    # download 默认为 false，不下载
)
```

### ❌ 错误用法

```python
# 错误 1：忘记设置 download=true，导致图片未下载
web-image-search(query="贵阳甲秀楼")
# 结果：只返回 URL，workspace/images/web_search/ 目录为空

# 错误 2：download 参数拼写错误
web-image-search(query="测试", downlaod=true)
# 结果：参数无效，不会下载

# 错误 3：analyze=true 但 download=false
web-image-search(query="测试", analyze=true, download=false)
# 结果：分析失败，没有本地图片可供分析
```

### 📂 图片保存位置

下载的图片默认保存到：
```
E:\auramate_hub\auramate-0.1\auramate-0.93\auramate\workspace\images\web_search\
```

### ⚠️ 常见问题排查

| 问题 | 原因 | 解决方案 |
|------|------|----------|
| 下载后目录为空 | `download` 参数未设置或为 false | 必须设置 `download: true` |
| 图片数量不足 | 关键词太冷门或网络问题 | 尝试更换关键词或增加 `limit` |
| 分析失败 | `download=false` 时无法分析 | 先设置 `download=true` 再分析 |
| 403 错误 | 百度反爬虫限制 | 工具已内置绕过逻辑，重试即可 |
| 图片<2KB 被删除 | 下载到的是缩略图或无效文件 | 更换关键词或增加 `limit` |

### 💡 最佳实践

1. **在 PPT/Word 中使用图片**：必须设置 `download: true`
2. **验证图片内容**：下载后用 `analyze: true` 或 `visual-analysis` 技能验证
3. **批量下载**：设置 `limit: 10` 或更高，获取更多选择
4. **中文关键词**：直接使用中文，工具会自动处理编码
