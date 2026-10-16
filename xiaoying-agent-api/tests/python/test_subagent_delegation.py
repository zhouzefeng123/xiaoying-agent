import json
import os
import sys
import time
import uuid
import argparse
from dataclasses import dataclass

import requests


BASE_URL = os.environ.get("AURAMATE_BASE_URL", "http://localhost:3000")
MODEL = os.environ.get("AURAMATE_MODEL", "aura-coder")
WORKSPACE_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../../workspace"))
REPORT_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "../reports"))


@dataclass
class ExpectedFile:
    path: str
    snippets: list[str]


@dataclass
class Scenario:
    name: str
    prompt: str
    required_tools: list[str]
    expected_files: list[ExpectedFile]
    required_content_snippets: list[str]


def print_step(message):
    print(f"\n{'=' * 60}\n[STEP] {message}\n{'=' * 60}")


def cleanup_paths(paths):
    for path in paths:
        if os.path.exists(path):
            os.remove(path)


def collect_stream_result(prompt):
    url = f"{BASE_URL}/v1/chat/completions"
    headers = {"Content-Type": "application/json"}
    payload = {
        "model": MODEL,
        "messages": [{"role": "user", "content": prompt}],
        "stream": True,
        "temperature": 0,
    }

    print(f"发送请求到 {url}")
    start_time = time.time()
    first_token_time = None
    content = ""
    input_tokens = 0
    output_tokens = 0
    tool_calls = []

    response = requests.post(url, headers=headers, data=json.dumps(payload), stream=True, timeout=(30, 300))
    response.raise_for_status()

    try:
        for line in response.iter_lines():
            if not line:
                continue
            decoded_line = line.decode("utf-8")
            if not decoded_line.startswith("data: "):
                continue
            json_str = decoded_line[6:]
            if json_str == "[DONE]":
                break
            try:
                chunk = json.loads(json_str)
            except json.JSONDecodeError:
                continue

            usage = chunk.get("usage") or {}
            input_tokens = usage.get("prompt_tokens", input_tokens)
            output_tokens = usage.get("completion_tokens", output_tokens)

            choices = chunk.get("choices") or []
            if not choices:
                continue
            delta = choices[0].get("delta") or {}

            text = delta.get("content")
            if text:
                if first_token_time is None:
                    first_token_time = time.time()
                content += text
                print(text, end="", flush=True)

            for tool_call in delta.get("tool_calls") or []:
                function_data = tool_call.get("function") or {}
                tool_name = function_data.get("name")
                if tool_name:
                    tool_calls.append(tool_name)
                    print(f"\n[TOOL CALL] {tool_name}")
    except (requests.exceptions.ConnectionError, requests.exceptions.ReadTimeout) as exc:
        if not content and not tool_calls:
            raise
        print(f"\n[WARN] 流式响应提前结束，使用已收集内容继续评估: {exc}", flush=True)

    end_time = time.time()
    return {
        "content": content,
        "tool_calls": tool_calls,
        "ttft_seconds": round((first_token_time - start_time) if first_token_time else 0, 4),
        "task_seconds": round(end_time - start_time, 4),
        "prompt_tokens": input_tokens,
        "completion_tokens": output_tokens,
    }


def verify_files(expected_files):
    missing = []
    invalid = []
    for item in expected_files:
        if not os.path.exists(item.path):
            missing.append(item.path)
            continue
        with open(item.path, "r", encoding="utf-8") as handle:
            content = handle.read()
        for snippet in item.snippets:
            if snippet not in content:
                invalid.append({"path": item.path, "snippet": snippet})
    return missing, invalid


def run_scenario(scenario):
    print_step(f"执行场景: {scenario.name}")
    print(f"提示词摘要: {scenario.prompt[:160]}...")
    result = collect_stream_result(scenario.prompt)

    print(f"\n\n[INFO] TTFT: {result['ttft_seconds']:.4f}s | Task Time: {result['task_seconds']:.4f}s")
    print(f"[INFO] Tokens: In={result['prompt_tokens']}, Out={result['completion_tokens']}")
    print(f"[INFO] Tools: {result['tool_calls']}")

    missing_tools = [tool for tool in scenario.required_tools if tool not in result["tool_calls"]]
    missing_files, invalid_files = verify_files(scenario.expected_files)
    missing_content = [snippet for snippet in scenario.required_content_snippets if snippet not in result["content"]]
    success = not missing_tools and not missing_files and not invalid_files and not missing_content

    if success:
        print(f"✅ [SUCCESS] 场景 {scenario.name} 通过")
    else:
        print(f"❌ [FAIL] 场景 {scenario.name} 失败")
        if missing_tools:
            print(f"  缺失工具调用: {missing_tools}")
        if missing_files:
            print(f"  缺失文件: {missing_files}")
        if invalid_files:
            print(f"  文件内容不匹配: {invalid_files}")
        if missing_content:
            print(f"  输出缺失内容: {missing_content}")

    return {
        "name": scenario.name,
        "success": success,
        "missing_tools": missing_tools,
        "missing_files": missing_files,
        "invalid_files": invalid_files,
        "missing_content": missing_content,
        **result,
    }


def build_scenarios():
    run_id = uuid.uuid4().hex[:8]
    target_tools_file = os.path.abspath(os.path.join(WORKSPACE_DIR, "../backend/internal/llm/agent/tools.go"))
    parallel_a = os.path.join(WORKSPACE_DIR, f"subagent_parallel_a_{run_id}.py")
    parallel_b = os.path.join(WORKSPACE_DIR, f"subagent_parallel_b_{run_id}.py")
    parallel_main = os.path.join(WORKSPACE_DIR, f"subagent_parallel_main_{run_id}.py")

    cleanup_paths([parallel_a, parallel_b, parallel_main])

    scenario_parallel = Scenario(
        name="并行委派写文件",
        prompt=(
            "你必须调用 batch_delegate_task 并行启动两个子智能体。"
            f"子任务1：在 workspace 下创建文件 subagent_parallel_a_{run_id}.py，内容必须包含 def value_a(): return 'A-{run_id}'。"
            f"子任务2：在 workspace 下创建文件 subagent_parallel_b_{run_id}.py，内容必须包含 def value_b(): return 'B-{run_id}'。"
            f"随后你必须调用 wait_for_workers 等待全部完成。wait_for_workers 返回后，无论子智能体是否声称已经写入，你都必须立即使用主智能体自己的 write 或 batch_write 工具在本地重写三个最终文件，且内容必须精确如下。"
            f"subagent_parallel_a_{run_id}.py 的最终内容必须是：def value_a(): return 'A-{run_id}'。"
            f"subagent_parallel_b_{run_id}.py 的最终内容必须是：def value_b(): return 'B-{run_id}'。"
            f"subagent_parallel_main_{run_id}.py 的最终内容必须精确包含两行导入 from subagent_parallel_a_{run_id} import value_a、from subagent_parallel_b_{run_id} import value_b，以及两行调用 print(value_a())、print(value_b())。"
            "不要生成 main() 包装函数，不要只复述子智能体结果，必须由主智能体亲自落盘最终文件。最终只输出简短总结。"
        ),
        required_tools=["batch_delegate_task", "wait_for_workers"],
        expected_files=[
            ExpectedFile(parallel_a, [f"def value_a():", f"A-{run_id}"]),
            ExpectedFile(parallel_b, [f"def value_b():", f"B-{run_id}"]),
            ExpectedFile(parallel_main, [f"from subagent_parallel_a_{run_id} import value_a", f"from subagent_parallel_b_{run_id} import value_b", "value_a()", "value_b()"]),
        ],
        required_content_snippets=[],
    )

    scenario_async = Scenario(
        name="异步委派与状态管理",
        prompt=(
            "你必须先调用 delegate_task，参数 async=true，role='analyst'。"
            f"委派内容：读取绝对路径 {target_tools_file}，找出当前注册的子智能体工具名称，并用不超过 4 行总结它们各自用途。"
            "拿到 worker_id 后，你必须调用 manage_worker，action='wait'，等待该 worker 完成。"
            "最终回答里必须明确出现 delegate_task、batch_delegate_task、manage_worker、wait_for_workers 这四个工具名。"
        ),
        required_tools=["delegate_task", "manage_worker"],
        expected_files=[],
        required_content_snippets=["delegate_task", "batch_delegate_task", "manage_worker", "wait_for_workers"],
    )

    return run_id, [scenario_parallel, scenario_async]


def parse_args():
    parser = argparse.ArgumentParser(description="Subagent delegation coverage test")
    parser.add_argument("--base-url", default=BASE_URL)
    parser.add_argument("--model", default=MODEL)
    parser.add_argument("--report-path", default="")
    return parser.parse_args()


def write_report(run_id, results, report_path=""):
    os.makedirs(REPORT_DIR, exist_ok=True)
    if not report_path:
        report_path = os.path.join(REPORT_DIR, f"subagent_delegation_report_{run_id}.json")
    with open(report_path, "w", encoding="utf-8") as handle:
        json.dump({"base_url": BASE_URL, "model": MODEL, "results": results}, handle, ensure_ascii=False, indent=2)
    print(f"\n[REPORT] 已写入 {report_path}")
    return report_path


def main():
    args = parse_args()
    global BASE_URL, MODEL
    BASE_URL = args.base_url
    MODEL = args.model
    run_id, scenarios = build_scenarios()
    results = [run_scenario(scenario) for scenario in scenarios]
    write_report(run_id, results, args.report_path)

    if all(item["success"] for item in results):
        sys.exit(0)
    sys.exit(1)


if __name__ == "__main__":
    main()
