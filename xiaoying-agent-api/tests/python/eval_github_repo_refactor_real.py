from __future__ import annotations

import argparse
import asyncio
import fnmatch
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import time
import uuid
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any
from urllib.request import urlopen

import websockets


DEFAULT_BASE_URL = "http://localhost:3000"
DEFAULT_MODEL = "aura-coder"
DEFAULT_MODE = "coding"
DEFAULT_TIMEOUT_SECONDS = 900
DEFAULT_MAX_TOTAL_DURATION_SECONDS = 1200
REPO_ROOT = Path(__file__).resolve().parents[3]
WORKSPACE_ROOT = REPO_ROOT / "workspace" / "github_repo_refactor_eval"
CACHE_ROOT = WORKSPACE_ROOT / "cache"
WORKTREE_ROOT = WORKSPACE_ROOT / "worktrees"
REPORTS_DIR = Path(__file__).resolve().parent / "reports"
FORBIDDEN_TOOLS = {"delegate_task", "manage_worker", "wait_for_workers"}


@dataclass
class ToolCallRecord:
    name: str
    input: str
    tool_execution_id: str = ""


@dataclass
class ToolResultRecord:
    name: str
    content: str
    is_error: bool
    tool_execution_id: str = ""


@dataclass
class TurnReport:
    label: str
    prompt: str
    session_id: str = ""
    duration_seconds: float = 0.0
    final_text: str = ""
    tool_calls: list[ToolCallRecord] = field(default_factory=list)
    tool_results: list[ToolResultRecord] = field(default_factory=list)
    done_received: bool = False
    error_event: str = ""


@dataclass
class VerificationResult:
    command: str
    return_code: int
    stdout: str
    stderr: str


@dataclass(frozen=True)
class GithubRefactorProfile:
    case_id: str
    repo_url: str
    repo_ref: str
    task_file: str
    follow_up_file: str = ""
    follow_up_files: tuple[str, ...] = ()
    verify_commands: tuple[str, ...] = ()
    expected_paths: tuple[str, ...] = ()
    allowed_changed_paths: tuple[str, ...] = ()
    required_content_markers: tuple[tuple[str, tuple[str, ...]], ...] = ()
    forbidden_content_markers: tuple[tuple[str, tuple[str, ...]], ...] = ()
    min_changed_files: int = 1
    max_changed_files: int = 0
    min_turns: int = 2
    min_total_tool_calls: int = 0
    min_distinct_read_paths: int = 0
    require_verification_each_turn: bool = False
    allow_subagents: bool = False
    timeout: int = DEFAULT_TIMEOUT_SECONDS
    max_total_duration: int = DEFAULT_MAX_TOTAL_DURATION_SECONDS


@dataclass
class EvaluationReport:
    scenario: str
    profile_path: str
    repo_url: str
    repo_ref: str
    source_commit: str
    fixture_root: str
    task_file: str
    session_id: str
    ws_url: str
    model: str
    mode: str
    duration_seconds: float
    final_text: str
    passed: bool
    failure_reasons: list[str] = field(default_factory=list)
    diagnostics: list[str] = field(default_factory=list)
    turn_reports: list[TurnReport] = field(default_factory=list)
    tool_calls: list[ToolCallRecord] = field(default_factory=list)
    tool_results: list[ToolResultRecord] = field(default_factory=list)
    changed_files: list[str] = field(default_factory=list)
    unexpected_changed_files: list[str] = field(default_factory=list)
    read_expected_paths: list[str] = field(default_factory=list)
    verification_turn_labels: list[str] = field(default_factory=list)
    verification_results: list[VerificationResult] = field(default_factory=list)
    session_reused: bool = False
    first_write_after_read: bool = False
    agent_ran_verification: bool = False
    follow_up_made_progress: bool = False


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="GitHub 真实仓库重构评估")
    parser.add_argument("--profile", default="", help="可选：JSON profile，提供固定仓库、任务和验证参数")
    parser.add_argument("--repo-url", default="", help="GitHub 仓库 clone URL")
    parser.add_argument("--repo-ref", default="", help="分支、tag 或 commit")
    parser.add_argument("--task-file", default="", help="包含重构任务说明的 Markdown 或文本文件")
    parser.add_argument("--case-id", default="")
    parser.add_argument("--follow-up-file", default="", help="可选：第二轮任务提示文件")
    parser.add_argument("--verify-command", action="append", default=[], help="可重复：完成后在仓库根目录执行的验证命令")
    parser.add_argument("--expected-path", action="append", default=[], help="可重复：期望被修改的相对路径")
    parser.add_argument("--allowed-changed-path", action="append", default=[], help="可重复：允许被修改的相对路径或 glob")
    parser.add_argument("--min-changed-files", type=int, default=1)
    parser.add_argument("--max-changed-files", type=int, default=0, help="0 表示不限制")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument("--mode", default=DEFAULT_MODE)
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT_SECONDS)
    parser.add_argument("--max-total-duration", type=int, default=DEFAULT_MAX_TOTAL_DURATION_SECONDS)
    parser.add_argument("--allow-subagents", action="store_true")
    parser.add_argument("--prepare-cache-only", action="store_true", help="只预热固定 ref 到本地缓存，不启动 agent")
    parser.add_argument("--offline", action="store_true", help="只允许使用本地缓存；目标 ref 不存在时直接失败，不访问远端")
    parser.add_argument("--keep-worktree", action="store_true")
    parser.add_argument("--report-path", default="")
    return parser.parse_args()


def resolve_profile_path(path_text: str, *, profile_path: Path | None = None) -> str:
    if not path_text:
        return ""
    path = Path(path_text)
    if path.is_absolute() or profile_path is None:
        return str(path)
    return str((profile_path.parent / path).resolve())


def load_profile(path_text: str) -> tuple[GithubRefactorProfile, Path]:
    profile_path = Path(path_text)
    if not profile_path.is_absolute():
        profile_path = (REPO_ROOT / profile_path).resolve()
    payload = json.loads(profile_path.read_text(encoding="utf-8"))
    follow_up_files = payload.get("follow_up_files", [])
    if not follow_up_files and payload.get("follow_up_file"):
        follow_up_files = [payload["follow_up_file"]]
    profile = GithubRefactorProfile(
        case_id=str(payload["case_id"]),
        repo_url=str(payload["repo_url"]),
        repo_ref=str(payload["repo_ref"]),
        task_file=resolve_profile_path(str(payload["task_file"]), profile_path=profile_path),
        follow_up_file=resolve_profile_path(str(payload.get("follow_up_file", "")), profile_path=profile_path),
        follow_up_files=tuple(
            resolve_profile_path(str(item), profile_path=profile_path) for item in follow_up_files
        ),
        verify_commands=tuple(str(item) for item in payload.get("verify_commands", [])),
        expected_paths=tuple(str(item) for item in payload.get("expected_paths", [])),
        allowed_changed_paths=tuple(str(item) for item in payload.get("allowed_changed_paths", [])),
        required_content_markers=tuple(
            (str(path), tuple(str(marker) for marker in markers))
            for path, markers in payload.get("required_content_markers", {}).items()
        ),
        forbidden_content_markers=tuple(
            (str(path), tuple(str(marker) for marker in markers))
            for path, markers in payload.get("forbidden_content_markers", {}).items()
        ),
        min_changed_files=int(payload.get("min_changed_files", 1)),
        max_changed_files=int(payload.get("max_changed_files", 0)),
        min_turns=int(payload.get("min_turns", 2)),
        min_total_tool_calls=int(payload.get("min_total_tool_calls", 0)),
        min_distinct_read_paths=int(payload.get("min_distinct_read_paths", 0)),
        require_verification_each_turn=bool(payload.get("require_verification_each_turn", False)),
        allow_subagents=bool(payload.get("allow_subagents", False)),
        timeout=int(payload.get("timeout", DEFAULT_TIMEOUT_SECONDS)),
        max_total_duration=int(payload.get("max_total_duration", DEFAULT_MAX_TOTAL_DURATION_SECONDS)),
    )
    return profile, profile_path


def apply_profile(args: argparse.Namespace) -> argparse.Namespace:
    profile: GithubRefactorProfile | None = None
    profile_path = ""
    if args.profile:
        profile, resolved_profile_path = load_profile(args.profile)
        profile_path = str(resolved_profile_path)

    def choose(cli_value: Any, profile_value: Any) -> Any:
        if isinstance(cli_value, str):
            return cli_value if cli_value else profile_value
        if isinstance(cli_value, list):
            return cli_value if cli_value else list(profile_value)
        return cli_value

    if profile is not None:
        args.case_id = choose(args.case_id, profile.case_id)
        args.repo_url = choose(args.repo_url, profile.repo_url)
        args.repo_ref = choose(args.repo_ref, profile.repo_ref)
        args.task_file = choose(args.task_file, profile.task_file)
        args.follow_up_file = choose(args.follow_up_file, profile.follow_up_file)
        args.follow_up_files = list(profile.follow_up_files)
        args.verify_command = choose(args.verify_command, profile.verify_commands)
        args.expected_path = choose(args.expected_path, profile.expected_paths)
        args.allowed_changed_path = choose(args.allowed_changed_path, profile.allowed_changed_paths)
        args.required_content_marker = list(profile.required_content_markers)
        args.forbidden_content_marker = list(profile.forbidden_content_markers)
        if args.min_changed_files == 1:
            args.min_changed_files = profile.min_changed_files
        if args.max_changed_files == 0:
            args.max_changed_files = profile.max_changed_files
        args.min_turns = profile.min_turns
        args.min_total_tool_calls = profile.min_total_tool_calls
        args.min_distinct_read_paths = profile.min_distinct_read_paths
        args.require_verification_each_turn = profile.require_verification_each_turn
        if args.timeout == DEFAULT_TIMEOUT_SECONDS:
            args.timeout = profile.timeout
        if args.max_total_duration == DEFAULT_MAX_TOTAL_DURATION_SECONDS:
            args.max_total_duration = profile.max_total_duration
        if not args.allow_subagents:
            args.allow_subagents = profile.allow_subagents

    missing = [name for name in ("repo_url", "repo_ref", "task_file") if not getattr(args, name)]
    if missing:
        missing_args = ", ".join(f"--{name.replace('_', '-')}" for name in missing)
        raise ValueError(f"缺少必要参数: {missing_args}；可直接传参，或通过 --profile 提供")

    if not args.case_id:
        args.case_id = "github_repo_refactor"
    if not hasattr(args, "required_content_marker"):
        args.required_content_marker = []
    if not hasattr(args, "forbidden_content_marker"):
        args.forbidden_content_marker = []
    if not hasattr(args, "follow_up_files"):
        args.follow_up_files = [args.follow_up_file] if args.follow_up_file else []
    if not hasattr(args, "min_turns"):
        args.min_turns = 2
        args.min_total_tool_calls = 0
        args.min_distinct_read_paths = 0
        args.require_verification_each_turn = False
    args.profile_path = profile_path
    return args


def ensure_backend_ready(base_url: str) -> None:
    with urlopen(f"{base_url}/v1/models", timeout=10) as response:
        if response.status != 200:
            raise RuntimeError(f"后端未就绪，状态码: {response.status}")


def build_ws_url(base_url: str) -> str:
    if base_url.startswith("https://"):
        return "wss://" + base_url[len("https://") :] + "/v1/ws"
    if base_url.startswith("http://"):
        return "ws://" + base_url[len("http://") :] + "/v1/ws"
    return base_url.rstrip("/") + "/v1/ws"


def run_command(command: list[str], *, cwd: Path | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        cwd=cwd,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=False,
    )


def run_git(*args: str, cwd: Path | None = None) -> str:
    completed = run_command(["git", *args], cwd=cwd)
    if completed.returncode != 0:
        raise RuntimeError(
            f"git {' '.join(args)} 失败 ({completed.returncode}): "
            f"{completed.stderr.strip() or completed.stdout.strip()}"
        )
    return completed.stdout.strip()


def run_git_with_retry(*args: str, cwd: Path | None = None, attempts: int = 3) -> str:
    last_error: RuntimeError | None = None
    for attempt in range(1, attempts + 1):
        try:
            return run_git(*args, cwd=cwd)
        except RuntimeError as err:
            last_error = err
            if attempt == attempts:
                raise
            time.sleep(attempt)
    raise last_error or RuntimeError("git command failed")


def cache_key(repo_url: str) -> str:
    digest = hashlib.sha256(repo_url.encode("utf-8")).hexdigest()[:12]
    repo_name = Path(repo_url.rstrip("/")).stem or "repo"
    return f"{repo_name}-{digest}"


def fetch_ref_to_cache(cache_dir: Path, repo_ref: str) -> str:
    ref_digest = hashlib.sha256(repo_ref.encode("utf-8")).hexdigest()[:16]
    local_ref = f"refs/eval/{ref_digest}"
    run_git_with_retry(
        "-C",
        str(cache_dir),
        "fetch",
        "--depth",
        "1",
        "--force",
        "origin",
        f"{repo_ref}:{local_ref}",
    )
    return run_git("-C", str(cache_dir), "rev-parse", local_ref)


def ensure_cached_repo(repo_url: str, repo_ref: str, *, offline: bool = False) -> tuple[Path, str]:
    CACHE_ROOT.mkdir(parents=True, exist_ok=True)
    cache_dir = CACHE_ROOT / cache_key(repo_url)
    if not cache_dir.exists():
        run_git("init", "--bare", str(cache_dir))
        run_git("-C", str(cache_dir), "remote", "add", "origin", repo_url)
    remote_url = run_git("-C", str(cache_dir), "remote", "get-url", "origin")
    if remote_url != repo_url:
        raise RuntimeError(f"缓存仓库 remote 不匹配: {remote_url} != {repo_url}")
    commit_check = run_command(["git", "-C", str(cache_dir), "rev-parse", "--verify", f"{repo_ref}^{{commit}}"])
    if commit_check.returncode == 0:
        source_commit = commit_check.stdout.strip()
        tree_check = run_command(["git", "-C", str(cache_dir), "cat-file", "-e", f"{source_commit}^{{tree}}"])
        if tree_check.returncode != 0:
            if offline:
                raise RuntimeError(f"离线模式下缓存中的目标 ref 缺少 tree 对象: {repo_ref}")
            source_commit = fetch_ref_to_cache(cache_dir, repo_ref)
    else:
        if offline:
            raise RuntimeError(f"离线模式下缓存缺少目标 ref: {repo_ref}")
        source_commit = fetch_ref_to_cache(cache_dir, repo_ref)
    tree_check = run_command(["git", "-C", str(cache_dir), "cat-file", "-e", f"{source_commit}^{{tree}}"])
    if tree_check.returncode != 0:
        raise RuntimeError(f"缓存中的目标 ref 缺少 tree 对象: {repo_ref}")
    return cache_dir, source_commit


def prepare_worktree(repo_url: str, repo_ref: str, case_id: str, *, offline: bool = False) -> tuple[Path, str]:
    cache_dir, source_commit = ensure_cached_repo(repo_url, repo_ref, offline=offline)
    WORKTREE_ROOT.mkdir(parents=True, exist_ok=True)
    run_id = uuid.uuid4().hex[:8]
    worktree = WORKTREE_ROOT / f"{case_id}_{run_id}"
    run_git("clone", "--no-local", "--no-checkout", str(cache_dir), str(worktree))
    run_git("-C", str(worktree), "fetch", "origin", f"{source_commit}:refs/eval/source")
    run_git("-C", str(worktree), "checkout", "--detach", source_commit)
    return worktree, source_commit


def read_text_file(path_text: str) -> str:
    path = Path(path_text)
    return path.read_text(encoding="utf-8").strip()


def build_initial_prompt(
    *,
    worktree: Path,
    repo_url: str,
    repo_ref: str,
    source_commit: str,
    task_text: str,
    expected_paths: list[str],
    verify_commands: list[str],
    allow_subagents: bool,
) -> str:
    verify_text = "；".join(verify_commands) if verify_commands else "无"
    expected_text = "；".join(expected_paths) if expected_paths else "未配置"
    subagent_rule = "允许按需使用子 agent。" if allow_subagents else "不要使用 delegate_task、manage_worker 或 wait_for_workers。"
    return (
        f"请在真实 GitHub 仓库中完成一次重构评估。仓库根目录：{worktree}。"
        f"来源：{repo_url}@{repo_ref}，实际 commit：{source_commit}。"
        "先阅读再修改，保持公开 API 与项目约定。"
        f"{subagent_rule}"
        f"本评测要求最终必须有有意义修改的路径：{expected_text}。"
        "不要只阅读测试文件；如果测试路径在清单中，必须补充或强化测试。"
        f"每个任务轮次结束前都必须亲自逐条执行这些完整验证命令：{verify_text}。"
        "运行 Flask 测试前请确保环境变量 FLASK_SKIP_DOTENV=1，避免父目录 .env 干扰。"
        "最后给出精炼摘要，说明修改文件、验证结果和剩余风险。"
        "\n\n"
        f"{task_text}"
    )


def build_follow_up_prompt(custom_follow_up: str) -> str:
    if custom_follow_up.strip():
        return custom_follow_up.strip()
    return (
        "继续。复查刚才的重构是否仍有重复逻辑、遗漏调用方或测试缺口；"
        "如果有，继续修正。然后重新执行验证命令，并给出最终摘要。"
    )


def build_review_snapshot(
    *,
    worktree: Path,
    expected_paths: list[str],
    allowed_changed_paths: list[str],
    verify_commands: list[str],
    required_content_markers: list[tuple[str, tuple[str, ...]]],
    forbidden_content_markers: list[tuple[str, tuple[str, ...]]],
) -> str:
    changed = changed_files(worktree)
    missing_expected = sorted(set(expected_paths) - set(changed))
    unexpected = [
        path for path in changed if allowed_changed_paths and not path_matches_any(path, allowed_changed_paths)
    ]
    lines = [
        "",
        "## 机器审查快照",
        f"- 当前已修改文件: {changed if changed else '无'}",
        f"- 仍缺少的期望修改文件: {missing_expected if missing_expected else '无'}",
        f"- 未允许的修改路径: {unexpected if unexpected else '无'}",
    ]
    marker_failures: list[str] = []
    for relative_path, markers in required_content_markers:
        content = read_file_text(worktree, relative_path)
        missing_markers = [marker for marker in markers if not re.search(marker, content, flags=re.MULTILINE)]
        if missing_markers:
            marker_failures.append(f"{relative_path} 缺少 {missing_markers}")
    for relative_path, markers in forbidden_content_markers:
        content = read_file_text(worktree, relative_path)
        present_markers = [marker for marker in markers if re.search(marker, content, flags=re.MULTILINE)]
        if present_markers:
            marker_failures.append(f"{relative_path} 仍包含 {present_markers}")
    lines.append(f"- 结构标记问题: {marker_failures if marker_failures else '无'}")
    if verify_commands:
        lines.append("- 本轮结束前你必须亲自逐条运行这些完整验证命令，不能只运行子集:")
        lines.extend(f"  - {command}" for command in verify_commands)
        lines.append("- 运行 Flask 测试前请设置 FLASK_SKIP_DOTENV=1，避免父目录 .env 干扰。")
    lines.append("如果缺少期望修改文件，先补齐这些文件，尤其不要漏掉测试文件。")
    lines.append("请基于以上审查继续修复；如果验证失败，先读报错附近代码和当前 diff，再做最小补丁并重新运行完整验证。")
    return "\n".join(lines)


def build_reviewed_follow_up_prompt(
    *,
    custom_follow_up: str,
    worktree: Path,
    expected_paths: list[str],
    allowed_changed_paths: list[str],
    verify_commands: list[str],
    required_content_markers: list[tuple[str, tuple[str, ...]]],
    forbidden_content_markers: list[tuple[str, tuple[str, ...]]],
) -> str:
    return (
        build_review_snapshot(
            worktree=worktree,
            expected_paths=expected_paths,
            allowed_changed_paths=allowed_changed_paths,
            verify_commands=verify_commands,
            required_content_markers=required_content_markers,
            forbidden_content_markers=forbidden_content_markers,
        )
        + "\n\n## 本轮追加要求\n"
        + build_follow_up_prompt(custom_follow_up)
    )


def build_payload(prompt: str, model: str, mode: str, session_id: str = "") -> dict[str, Any]:
    payload: dict[str, Any] = {"type": "chat", "content": prompt, "model": model, "mode": mode}
    if session_id:
        payload["session_id"] = session_id
    return payload


async def run_turn(args: argparse.Namespace, prompt: str, label: str, session_id: str = "") -> TurnReport:
    ws_url = build_ws_url(args.base_url)
    turn = TurnReport(label=label, prompt=prompt, session_id=session_id)
    started_at = time.time()
    async with websockets.connect(ws_url, max_size=8 * 1024 * 1024) as websocket:
        await websocket.send(json.dumps(build_payload(prompt, args.model, args.mode, session_id), ensure_ascii=False))
        deadline = time.time() + args.timeout
        while time.time() < deadline:
            try:
                raw = await asyncio.wait_for(websocket.recv(), timeout=min(2.0, max(0.1, deadline - time.time())))
            except asyncio.TimeoutError:
                continue
            event = json.loads(raw)
            event_type = event.get("type", "")
            event_session_id = str(event.get("session_id", "") or "")
            if event_session_id and not turn.session_id:
                turn.session_id = event_session_id
            if event_type == "token":
                turn.final_text += str(event.get("content", ""))
            elif event_type == "tool_call":
                try:
                    tool_calls = json.loads(event.get("content", "[]"))
                except json.JSONDecodeError:
                    tool_calls = []
                for tool_call in tool_calls:
                    turn.tool_calls.append(
                        ToolCallRecord(
                            name=str(tool_call.get("name", "") or ""),
                            input=str(tool_call.get("input", "") or ""),
                            tool_execution_id=str(event.get("tool_execution_id", "") or ""),
                        )
                    )
            elif event_type == "tool_result":
                try:
                    data = json.loads(event.get("content", "{}"))
                except json.JSONDecodeError:
                    data = {}
                turn.tool_results.append(
                    ToolResultRecord(
                        name=str(data.get("name", "") or ""),
                        content=str(data.get("content", "") or ""),
                        is_error=bool(data.get("is_error", False)),
                        tool_execution_id=str(event.get("tool_execution_id", "") or ""),
                    )
                )
            elif event_type == "done":
                turn.done_received = True
                break
            elif event_type == "error":
                turn.error_event = str(event.get("content", "") or event.get("error", "") or "unknown error")
                break
    turn.duration_seconds = time.time() - started_at
    return turn


def changed_files(root: Path) -> list[str]:
    output = run_git("-C", str(root), "status", "--short")
    changed: list[str] = []
    for line in output.splitlines():
        if not line.strip():
            continue
        if len(line) >= 4 and line[2] == " ":
            path_text = line[3:]
        else:
            path_text = line[2:].strip()
        if " -> " in path_text:
            path_text = path_text.rsplit(" -> ", 1)[1]
        changed.append(path_text.strip())
    return changed


def path_matches_any(relative_path: str, patterns: list[str]) -> bool:
    normalized = relative_path.replace("\\", "/")
    for pattern in patterns:
        normalized_pattern = pattern.replace("\\", "/")
        if normalized == normalized_pattern or fnmatch.fnmatch(normalized, normalized_pattern):
            return True
    return False


def tool_call_mentions_path(call: ToolCallRecord, relative_path: str) -> bool:
    normalized_input = call.input.replace("\\\\", "/").replace("\\", "/")
    normalized_path = relative_path.replace("\\", "/")
    return normalized_path in normalized_input


def is_read_tool(call: ToolCallRecord) -> bool:
    return call.name in {"read", "read_file", "grep", "ls", "repo_map", "lookup_symbol"}


def is_write_tool(call: ToolCallRecord) -> bool:
    return call.name in {"write", "write_file", "edit", "patch", "batch_write", "apply_patch"}


def command_matches_verification(call_input: str, verify_commands: list[str]) -> bool:
    lowered = call_input.lower()
    return any(command.lower() in lowered for command in verify_commands)


def read_file_text(root: Path, relative_path: str) -> str:
    path = root / relative_path
    return path.read_text(encoding="utf-8") if path.exists() else ""


def run_verification(root: Path, command: str) -> VerificationResult:
    env = dict(os.environ)
    env.setdefault("FLASK_SKIP_DOTENV", "1")
    completed = subprocess.run(
        command,
        cwd=root,
        shell=True,
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    return VerificationResult(
        command=command,
        return_code=completed.returncode,
        stdout=completed.stdout,
        stderr=completed.stderr,
    )


def evaluate_report(
    report: EvaluationReport,
    *,
    expected_paths: list[str],
    allowed_changed_paths: list[str],
    min_changed_files: int,
    max_changed_files: int,
    min_turns: int,
    min_total_tool_calls: int,
    min_distinct_read_paths: int,
    require_verification_each_turn: bool,
    verify_commands: list[str],
    allow_subagents: bool,
    max_total_duration: int,
    required_content_markers: list[tuple[str, tuple[str, ...]]],
    forbidden_content_markers: list[tuple[str, tuple[str, ...]]],
) -> EvaluationReport:
    report.tool_calls = [call for turn in report.turn_reports for call in turn.tool_calls]
    report.tool_results = [result for turn in report.turn_reports for result in turn.tool_results]
    report.session_reused = (
        len(report.turn_reports) > 1
        and bool(report.session_id)
        and all(turn.session_id == report.session_id for turn in report.turn_reports[1:])
    )
    if not report.turn_reports or not all(turn.done_received for turn in report.turn_reports):
        report.failure_reasons.append("未收到全部 done 事件")
    if any(turn.error_event for turn in report.turn_reports):
        report.failure_reasons.append("收到 error 事件")
    if len(report.turn_reports) > 1 and not report.session_reused:
        report.failure_reasons.append("继续轮未复用同一 session")
    if len(report.turn_reports) < min_turns:
        report.failure_reasons.append(f"任务轮次数不足: {len(report.turn_reports)} < {min_turns}")
    if report.duration_seconds > max_total_duration:
        report.failure_reasons.append(
            f"真实仓库重构耗时过长: {report.duration_seconds:.2f}s > {max_total_duration}s"
        )
    if not allow_subagents:
        forbidden = sorted({call.name for call in report.tool_calls if call.name in FORBIDDEN_TOOLS})
        if forbidden:
            report.failure_reasons.append(f"单主 agent 场景出现禁止工具: {forbidden}")
    tool_error_count = sum(1 for result in report.tool_results if result.is_error)
    if tool_error_count:
        report.diagnostics.append(f"工具执行结果中存在错误: {tool_error_count}")
    if len(report.tool_calls) < min_total_tool_calls:
        report.failure_reasons.append(f"工具调用数不足: {len(report.tool_calls)} < {min_total_tool_calls}")

    report.changed_files = changed_files(Path(report.fixture_root))
    if len(report.changed_files) < min_changed_files:
        report.failure_reasons.append(
            f"修改文件数不足: {len(report.changed_files)} < {min_changed_files}"
        )
    if max_changed_files > 0 and len(report.changed_files) > max_changed_files:
        report.failure_reasons.append(
            f"修改文件数过多: {len(report.changed_files)} > {max_changed_files}"
        )
    missing_expected_paths = sorted(set(expected_paths) - set(report.changed_files))
    if missing_expected_paths:
        report.failure_reasons.append(f"未修改期望路径: {missing_expected_paths}")
    if allowed_changed_paths:
        report.unexpected_changed_files = [
            path for path in report.changed_files if not path_matches_any(path, allowed_changed_paths)
        ]
        if report.unexpected_changed_files:
            report.failure_reasons.append(f"出现未允许的修改路径: {report.unexpected_changed_files}")

    first_write_index = next((index for index, call in enumerate(report.tool_calls) if is_write_tool(call)), None)
    report.read_expected_paths = sorted(
        {
            path
            for call in report.tool_calls
            if is_read_tool(call)
            for path in expected_paths
            if tool_call_mentions_path(call, path)
        }
    )
    if len(report.read_expected_paths) < min_distinct_read_paths:
        report.failure_reasons.append(
            f"读取预期路径覆盖不足: {len(report.read_expected_paths)} < {min_distinct_read_paths}"
        )
    expected_reads_before_write = {
        path
        for index, call in enumerate(report.tool_calls)
        if first_write_index is not None
        and index < first_write_index
        and is_read_tool(call)
        for path in expected_paths
        if tool_call_mentions_path(call, path)
    }
    report.first_write_after_read = first_write_index is not None and set(expected_paths).issubset(expected_reads_before_write)
    if not report.first_write_after_read:
        report.failure_reasons.append("首次写入前未完整阅读期望修改文件")

    verification_tool_calls = [
        call
        for call in report.tool_calls
        if call.name in {"run_command", "bash"} and command_matches_verification(call.input, verify_commands)
    ]
    report.agent_ran_verification = bool(verification_tool_calls)
    report.verification_turn_labels = [
        turn.label
        for turn in report.turn_reports
        if any(
            call.name in {"run_command", "bash"} and command_matches_verification(call.input, verify_commands)
            for call in turn.tool_calls
        )
    ]
    if verify_commands and not report.agent_ran_verification:
        report.failure_reasons.append("agent 未主动执行配置的验证命令")
    if require_verification_each_turn and len(report.verification_turn_labels) < len(report.turn_reports):
        missing = [
            turn.label for turn in report.turn_reports if turn.label not in set(report.verification_turn_labels)
        ]
        report.failure_reasons.append(f"这些任务轮次未主动执行验证命令: {missing}")

    if len(report.turn_reports) > 1:
        follow_up_made_progress = []
        for turn in report.turn_reports[1:]:
            ran_verification = any(
                call.name in {"run_command", "bash"} and command_matches_verification(call.input, verify_commands)
                for call in turn.tool_calls
            )
            made_substantive_change = any(is_write_tool(call) for call in turn.tool_calls)
            follow_up_made_progress.append(
                bool(turn.tool_calls)
                and (ran_verification if require_verification_each_turn else (made_substantive_change or ran_verification))
            )
        report.follow_up_made_progress = all(follow_up_made_progress)
        if not report.follow_up_made_progress:
            report.failure_reasons.append("至少一个续接轮未继续执行实质修复或重新验证")

    root = Path(report.fixture_root)
    for relative_path, markers in required_content_markers:
        content = read_file_text(root, relative_path)
        missing_markers = [marker for marker in markers if not re.search(marker, content, flags=re.MULTILINE)]
        if missing_markers:
            report.failure_reasons.append(f"{relative_path} 缺少必要结构: {missing_markers}")
    for relative_path, markers in forbidden_content_markers:
        content = read_file_text(root, relative_path)
        present_markers = [marker for marker in markers if re.search(marker, content, flags=re.MULTILINE)]
        if present_markers:
            report.failure_reasons.append(f"{relative_path} 仍包含禁止结构: {present_markers}")

    report.verification_results = [run_verification(Path(report.fixture_root), command) for command in verify_commands]
    failed_commands = [item.command for item in report.verification_results if item.return_code != 0]
    if failed_commands:
        report.failure_reasons.append(f"验证命令失败: {failed_commands}")
    if not verify_commands:
        report.diagnostics.append("未配置验证命令，结果只能证明发生了修改，不能证明行为正确")

    report.passed = not report.failure_reasons
    return report


def save_report(path: Path, report: EvaluationReport) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(asdict(report), ensure_ascii=False, indent=2), encoding="utf-8")


async def run_evaluation(args: argparse.Namespace) -> EvaluationReport:
    task_text = read_text_file(args.task_file)
    follow_up_texts = [read_text_file(path) for path in args.follow_up_files]
    if not follow_up_texts:
        follow_up_texts = [""]
    started_at = time.time()
    try:
        worktree, source_commit = prepare_worktree(args.repo_url, args.repo_ref, args.case_id, offline=args.offline)
    except Exception as err:
        return EvaluationReport(
            scenario=args.case_id,
            profile_path=args.profile_path,
            repo_url=args.repo_url,
            repo_ref=args.repo_ref,
            source_commit="",
            fixture_root="",
            task_file=str(Path(args.task_file)),
            session_id="",
            ws_url=build_ws_url(args.base_url),
            model=args.model,
            mode=args.mode,
            duration_seconds=time.time() - started_at,
            final_text="",
            passed=False,
            failure_reasons=["仓库准备失败"],
            diagnostics=[str(err)],
        )
    initial_prompt = build_initial_prompt(
        worktree=worktree,
        repo_url=args.repo_url,
        repo_ref=args.repo_ref,
        source_commit=source_commit,
        task_text=task_text,
        expected_paths=args.expected_path,
        verify_commands=args.verify_command,
        allow_subagents=args.allow_subagents,
    )
    turn_reports = [await run_turn(args, initial_prompt, "initial")]
    for index, follow_up_text in enumerate(follow_up_texts, start=1):
        turn_reports.append(
            await run_turn(
                args,
                build_reviewed_follow_up_prompt(
                    custom_follow_up=follow_up_text,
                    worktree=worktree,
                    expected_paths=args.expected_path,
                    allowed_changed_paths=args.allowed_changed_path,
                    verify_commands=args.verify_command,
                    required_content_markers=args.required_content_marker,
                    forbidden_content_markers=args.forbidden_content_marker,
                ),
                f"follow_up_{index}",
                turn_reports[0].session_id,
            )
        )
    report = EvaluationReport(
        scenario=args.case_id,
        profile_path=args.profile_path,
        repo_url=args.repo_url,
        repo_ref=args.repo_ref,
        source_commit=source_commit,
        fixture_root=str(worktree),
        task_file=str(Path(args.task_file)),
        session_id=turn_reports[0].session_id,
        ws_url=build_ws_url(args.base_url),
        model=args.model,
        mode=args.mode,
        duration_seconds=time.time() - started_at,
        final_text="\n".join(turn.final_text for turn in turn_reports),
        passed=False,
        turn_reports=turn_reports,
    )
    report = evaluate_report(
        report,
        expected_paths=args.expected_path,
        allowed_changed_paths=args.allowed_changed_path,
        min_changed_files=args.min_changed_files,
        max_changed_files=args.max_changed_files,
        min_turns=args.min_turns,
        min_total_tool_calls=args.min_total_tool_calls,
        min_distinct_read_paths=args.min_distinct_read_paths,
        require_verification_each_turn=args.require_verification_each_turn,
        verify_commands=args.verify_command,
        allow_subagents=args.allow_subagents,
        max_total_duration=args.max_total_duration,
        required_content_markers=args.required_content_marker,
        forbidden_content_markers=args.forbidden_content_marker,
    )
    if not args.keep_worktree and report.passed:
        shutil.rmtree(worktree, ignore_errors=True)
    return report


def main() -> int:
    args = apply_profile(parse_args())
    if args.prepare_cache_only:
        cache_dir, source_commit = ensure_cached_repo(args.repo_url, args.repo_ref, offline=args.offline)
        payload = {
            "repo_url": args.repo_url,
            "repo_ref": args.repo_ref,
            "source_commit": source_commit,
            "cache_dir": str(cache_dir),
            "offline": bool(args.offline),
        }
        print(json.dumps(payload, ensure_ascii=False))
        return 0
    ensure_backend_ready(args.base_url)
    report = asyncio.run(run_evaluation(args))
    target = Path(args.report_path) if args.report_path else REPORTS_DIR / f"{args.case_id}_{uuid.uuid4().hex[:8]}.json"
    save_report(target, report)
    print(target)
    if report.failure_reasons:
        for item in report.failure_reasons:
            print(f"- {item}")
    return 0 if report.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
