# OpenVINO 向量 Helper

这个目录用于构建 AuraMate 的本地向量执行器 `ov-embed.exe`。

目标：

- 不启动常驻服务
- 由 Go 后端按需拉起
- 使用 stdin/stdout 传递批量文本和向量结果

## 目录约定

- `src/main.cpp`：OpenVINO GenAI 文本向量 CLI
- `model/`：放置已经转换好的 OpenVINO embedding 模型目录内容
- `dist/`：打包阶段复制的最终目录

## 运行协议

启动参数：

```txt
ov-embed.exe --model-dir <MODEL_DIR> --device CPU
```

stdin 输入：

```json
{
  "texts": ["第一段文本", "第二段文本"],
  "maxLength": 512
}
```

stdout 输出：

```json
{
  "embeddings": [[0.1, 0.2], [0.3, 0.4]],
  "dimension": 2
}
```

错误时 stdout 仍会返回 JSON：

```json
{
  "error": "错误信息"
}
```

## 构建

请先准备好 OpenVINO GenAI CMake 包路径，然后执行：

```bat
build_windows.bat
```

建议同时设置这些环境变量：

- `OpenVINOGenAI_DIR` 或 `OPENVINO_GENAI_DIR`
- `OPENVINO_RUNTIME_BIN`：OpenVINO Runtime DLL 所在目录
- `OPENVINO_GENAI_RUNTIME_BIN`：OpenVINO GenAI DLL 所在目录

## 模型说明

运行时要求 `--model-dir` 指向一个完整的 OpenVINO Text Embedding 模型目录，目录中应包含：

- `openvino_model.xml`
- `openvino_model.bin`
- tokenizer 相关文件

如果 Go 端未显式指定目录，则会优先查找：

- `openvino_embed/model`
- `openvino_embed/models/<modelName>`

## 打包

后端打包脚本会优先复制 `backend/tools/openvino_embed/dist` 到 `release_bundle/openvino_embed`，再由 Electron 安装包一起带走。