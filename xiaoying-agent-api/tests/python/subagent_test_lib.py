from __future__ import annotations

import json
import os
import re
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import requests


DEFAULT_BASE_URL = os.getenv("AURAMATE_BASE_URL", "http://localhost:3000")
DEFAULT_MODEL = os.getenv("AURAMATE_MODEL", "aura-coder")
REQUEST_TIMEOUT_SECONDS = 600
REPO_ROOT = Path(__file__).resolve().parents[3]
WORKSPACE_DIR = REPO_ROOT / "workspace"
REPORT_DIR = REPO_ROOT / "backend" / "tests" / "reports"


@dataclass
class ExpectedFile:
    path: Path
    snippets: list[str] = field(default_factory=list)
    forbidden_snippets: list[str] = field(default_factory=list)


@dataclass
class ToolCallRecord:
    index: int
    name: str
    arguments: str
    raw: dict[str, Any] = field(default_factory=dict)

    def parsed_argument_items(self) -> list[dict[str, Any]]:
        return parse_json_objects(self.arguments)


def ensure_backend_ready(base_url: str) -> None:
    response = requests.get(f"{base_url}/v1/models", timeout=10)
    response.raise_for_status()


def print_step(message: str) -> None:
    print(f"\n{'=' * 88}\n{message}\n{'=' * 88}")


def cleanup_path(path: Path) -> None:
    if path.exists():
        if path.is_dir():
            for child in sorted(path.glob("**/*"), reverse=True):
                if child.is_file():
                    child.unlink()
                elif child.is_dir():
                    child.rmdir()
            path.rmdir()
        else:
            path.unlink()


def parse_json_objects(text: str) -> list[dict[str, Any]]:
    decoder = json.JSONDecoder()
    index = 0
    text = text.strip()
    items: list[dict[str, Any]] = []
    while index < len(text):
        while index < len(text) and text[index].isspace():
            index += 1
        if index >= len(text):
            break
        try:
            payload, next_index = decoder.raw_decode(text, index)
        except json.JSONDecodeError:
            return []
        if isinstance(payload, dict):
            items.append(payload)
        index = next_index
    return items


def normalize_tool_calls(buffers: dict[int, dict[str, Any]]) -> list[ToolCallRecord]:
    records: list[ToolCallRecord] = []
    for index in sorted(buffers):
        entry = buffers[index]
        name = str(entry.get("name", "") or "").strip()
        arguments = "".join(entry.get("arguments", [])).strip()
        if not name:
            continue
        parsed_items = parse_json_objects(arguments)
        if len(parsed_items) > 1:
            for offset, payload in enumerate(parsed_items):
                payload_text = json.dumps(payload, ensure_ascii=False)
                records.append(
                    ToolCallRecord(
                        index=index + offset,
                        name=name,
                        arguments=payload_text,
                        raw={"name": name, "arguments": payload_text},
                    )
                )
            continue
        records.append(
            ToolCallRecord(
                index=index,
                name=name,
                arguments=arguments,
                raw={"name": name, "arguments": arguments},
            )
        )
    return records


def build_tool_records(index: int, name: str, arguments: str, raw: dict[str, Any] | None = None) -> list[ToolCallRecord]:
    arguments = arguments.strip()
    payload = raw or {"name": name, "arguments": arguments}
    parsed_items = parse_json_objects(arguments)
    if len(parsed_items) > 1:
        records: list[ToolCallRecord] = []
        for offset, item in enumerate(parsed_items):
            payload_text = json.dumps(item, ensure_ascii=False)
            records.append(
                ToolCallRecord(
                    index=index + offset,
                    name=name,
                    arguments=payload_text,
                    raw={"name": name, "arguments": payload_text},
                )
            )
        return records
    return [ToolCallRecord(index=index, name=name, arguments=arguments, raw=payload)]


def ensure_expected_file_parents(expected_files: list[ExpectedFile]) -> None:
    for item in expected_files:
        item.path.parent.mkdir(parents=True, exist_ok=True)


def collect_stream_result(base_url: str, model: str, prompt: str, timeout_seconds: int) -> dict[str, Any]:
    url = f"{base_url}/v1/chat/completions"
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "stream": True,
        "temperature": 0,
    }
    headers = {"Content-Type": "application/json"}

    start_time = time.time()
    first_token_time = None
    content_parts: list[str] = []
    usage = {"prompt_tokens": 0, "completion_tokens": 0}
    tool_buffers: dict[int, dict[str, Any]] = {}
    tool_calls: list[ToolCallRecord] = []

    response = requests.post(
        url,
        headers=headers,
        data=json.dumps(payload, ensure_ascii=False),
        stream=True,
        timeout=(30, timeout_seconds),
    )
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

            raw_usage = chunk.get("usage") or {}
            usage["prompt_tokens"] = int(raw_usage.get("prompt_tokens", usage["prompt_tokens"]) or usage["prompt_tokens"])
            usage["completion_tokens"] = int(raw_usage.get("completion_tokens", usage["completion_tokens"]) or usage["completion_tokens"])

            choices = chunk.get("choices") or []
            if not choices:
                continue
            delta = choices[0].get("delta") or {}

            text = delta.get("content")
            if text:
                if first_token_time is None:
                    first_token_time = time.time()
                content_parts.append(text)
                print(text, end="", flush=True)

            for tool_call in delta.get("tool_calls") or []:
                function_data = tool_call.get("function") or {}
                name = str(function_data.get("name") or "").strip()
                arguments = str(function_data.get("arguments") or "")

                # The chat endpoint emits one complete tool_call per tool use event.
                # Record these directly so reused/missing indexes do not collapse distinct tools.
                if name and arguments.strip():
                    print(f"\n[TOOL CALL] {name}")
                    tool_calls.extend(build_tool_records(len(tool_calls), name, arguments, raw=tool_call))
                    continue

                index = int(tool_call.get("index", 0) or 0)
                buffer = tool_buffers.setdefault(index, {"arguments": []})
                if name:
                    buffer["name"] = name
                    print(f"\n[TOOL CALL] {name}")
                if arguments:
                    buffer.setdefault("arguments", []).append(arguments)
    except (requests.exceptions.ConnectionError, requests.exceptions.ReadTimeout) as exc:
        if not content_parts and not tool_calls and not tool_buffers:
            raise
        print(f"\n[WARN] 流式响应提前结束，使用已收集内容继续评估: {exc}", flush=True)

    end_time = time.time()
    tool_calls.extend(normalize_tool_calls(tool_buffers))
    return {
        "content": "".join(content_parts),
        "tool_calls": tool_calls,
        "tool_names": [item.name for item in tool_calls],
        "ttft_seconds": round((first_token_time - start_time) if first_token_time else 0, 4),
        "duration_seconds": round(end_time - start_time, 4),
        "prompt_tokens": usage["prompt_tokens"],
        "completion_tokens": usage["completion_tokens"],
    }


def verify_files(expected_files: list[ExpectedFile]) -> tuple[list[str], list[dict[str, str]]]:
    missing: list[str] = []
    invalid: list[dict[str, str]] = []
    for item in expected_files:
        if not item.path.exists():
            missing.append(str(item.path))
            continue
        content = item.path.read_text(encoding="utf-8")
        for snippet in item.snippets:
            if snippet not in content:
                invalid.append({"path": str(item.path), "snippet": snippet})
        for snippet in item.forbidden_snippets:
            if snippet in content:
                invalid.append({"path": str(item.path), "snippet": f"FORBIDDEN:{snippet}"})
    return missing, invalid


def validate_order(tool_names: list[str], ordered_tools: list[str]) -> str:
    if not ordered_tools:
        return ""
    position = 0
    for tool in tool_names:
        if tool == ordered_tools[position]:
            position += 1
            if position == len(ordered_tools):
                return ""
    return f"期望工具顺序 {ordered_tools}，实际 {tool_names}"


def has_tool_argument_snippet(tool_calls: list[ToolCallRecord], tool_name: str, snippet: str) -> bool:
    normalized_snippet = re.sub(r"\s+", "", snippet)
    for record in tool_calls:
        if record.name != tool_name:
            continue
        if snippet in record.arguments:
            return True
        if normalized_snippet and normalized_snippet in re.sub(r"\s+", "", record.arguments):
            return True
    return False


def count_tool_calls(tool_calls: list[ToolCallRecord], tool_name: str) -> int:
    return sum(1 for record in tool_calls if record.name == tool_name)


def write_standard_report(report_path: Path, payload: dict[str, Any]) -> None:
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")