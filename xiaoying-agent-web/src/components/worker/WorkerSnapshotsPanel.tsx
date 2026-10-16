import { useCallback, useEffect, useMemo, useState } from 'react';
import { AlertTriangle, Bot, RefreshCw, Timer, X } from 'lucide-react';

type WorkerSnapshot = {
	worker_id: string;
	role: string;
	task_id: string;
	status: string;
	last_activity_at: string;
	timeout_seconds: number;
	has_deadline: boolean;
	deadline_unix?: number;
	parent_session_id?: string;
	archived: boolean;
	archived_reason?: string;
	last_error?: string;
	failure_type?: string;
	retryable_failure: boolean;
	provider_retry_count?: number;
	output_retry_count?: number;
	last_recovery_note?: string;
	structured_status?: string;
	structured_summary?: string;
	last_result_preview?: string;
	completed_unix?: number;
};

type WorkerSnapshotsSummary = {
	total: number;
	running: number;
	pending: number;
	completed: number;
	failed: number;
	archived: number;
};

type WorkerSnapshotsResponse = {
	snapshots: WorkerSnapshot[];
	summary: WorkerSnapshotsSummary;
};

type WorkerSnapshotsPanelProps = {
	isOpen: boolean;
	onClose: () => void;
};

const POLL_INTERVAL_MS = 5000;

const isActiveStatus = (status: string) => status === 'running' || status === 'pending';

const isTerminalStatus = (status: string) => {
	switch (status) {
		case 'completed':
		case 'failed':
		case 'partial':
		case 'stopped':
		case 'cancelled':
		case 'timed_out':
		case 'timeout':
			return true;
		default:
			return false;
	}
};

const isFailureSnapshot = (snapshot: WorkerSnapshot) => {
	const status = snapshot.status.trim().toLowerCase();
	if (status === 'completed') {
		return false;
	}
	return status === 'failed' || Boolean(snapshot.failure_type);
};

const getStatusTone = (status: string, archived: boolean) => {
	if (archived) {
		return 'bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300';
	}
	switch (status) {
		case 'running':
			return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300';
		case 'failed':
			return 'bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300';
		case 'pending':
			return 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300';
		case 'completed':
			return 'bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300';
		default:
			return 'bg-zinc-100 text-zinc-700 dark:bg-zinc-900 dark:text-zinc-300';
	}
};

const formatWorkerRole = (role: string) => {
	switch (role.trim().toLowerCase()) {
		case 'coder':
			return '编码智能体';
		case 'planner':
			return '规划智能体';
		case 'researcher':
			return '检索智能体';
		case 'worker':
			return '执行智能体';
		case 'task':
			return '任务智能体';
		default:
			return role || '子智能体';
	}
};

const formatStatusLabel = (status: string, archived: boolean) => {
	const normalized = status.trim().toLowerCase();
	let label = status;
	switch (normalized) {
		case 'running':
			label = '运行中';
			break;
		case 'pending':
			label = '待处理';
			break;
		case 'completed':
			label = '已完成';
			break;
		case 'failed':
			label = '失败';
			break;
		case 'partial':
			label = '部分完成';
			break;
		case 'stopped':
			label = '已停止';
			break;
		case 'cancelled':
			label = '已取消';
			break;
		case 'timed_out':
		case 'timeout':
			label = '已超时';
			break;
	}
	return archived ? `${label} / 已归档` : label;
};

const formatFailureType = (failureType?: string) => {
	if (!failureType) return '异常';
	switch (failureType.trim().toLowerCase()) {
		case 'timeout':
		case 'timed_out':
			return '超时';
		case 'cancelled':
		case 'canceled':
			return '已取消';
		case 'provider_error':
			return '模型调用异常';
		case 'output_error':
		case 'output_validation_error':
			return '输出异常';
		case 'tool_error':
			return '工具异常';
		default:
			return failureType;
	}
};

const formatAgo = (iso: string, now: number) => {
	const ts = Date.parse(iso);
	if (Number.isNaN(ts)) return '未知';
	const delta = Math.max(0, Math.floor((now - ts) / 1000));
	if (delta < 60) return `${delta}s 前`;
	if (delta < 3600) return `${Math.floor(delta / 60)}m 前`;
	return `${Math.floor(delta / 3600)}h 前`;
};

const formatRemaining = (deadlineUnix?: number, nowMs?: number) => {
	if (!deadlineUnix || !nowMs) return '无';
	const delta = deadlineUnix - Math.floor(nowMs / 1000);
	if (delta <= 0) return '已超时';
	if (delta < 60) return `${delta}s`;
	if (delta < 3600) return `${Math.floor(delta / 60)}m ${delta % 60}s`;
	return `${Math.floor(delta / 3600)}h ${Math.floor((delta % 3600) / 60)}m`;
};

const formatRetrySummary = (snapshot: WorkerSnapshot) => {
	return `模型重试=${snapshot.provider_retry_count ?? 0} 输出重试=${snapshot.output_retry_count ?? 0} 可重试=${snapshot.retryable_failure ? '是' : '否'}`;
};

export function WorkerSnapshotsPanel({ isOpen, onClose }: WorkerSnapshotsPanelProps) {
	const [data, setData] = useState<WorkerSnapshotsResponse | null>(null);
	const [loading, setLoading] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [nowMs, setNowMs] = useState(() => Date.now());

	const loadSnapshots = useCallback(async () => {
		setLoading(true);
		try {
			const response = await fetch('/v1/swarm/snapshots?limit=200');
			if (!response.ok) {
				throw new Error(`请求失败：${response.status}`);
			}
			const nextData = await response.json() as WorkerSnapshotsResponse;
			setData(nextData);
			setError(null);
			setNowMs(Date.now());
		} catch (err) {
			const message = err instanceof Error ? err.message : '加载失败';
			setError(message);
		} finally {
			setLoading(false);
		}
	}, []);

	useEffect(() => {
		if (!isOpen) return;
		void loadSnapshots();
		const pollTimer = window.setInterval(() => {
			void loadSnapshots();
		}, POLL_INTERVAL_MS);
		const clockTimer = window.setInterval(() => {
			setNowMs(Date.now());
		}, 1000);
		return () => {
			window.clearInterval(pollTimer);
			window.clearInterval(clockTimer);
		};
	}, [isOpen, loadSnapshots]);

	const snapshots = data?.snapshots ?? [];
	const summary = data?.summary;
	const activeSnapshots = useMemo(() => {
		return snapshots.filter((snapshot) => isActiveStatus(snapshot.status));
	}, [snapshots]);

	const recentTerminalSnapshots = useMemo(() => {
		return snapshots.filter((snapshot) => isTerminalStatus(snapshot.status));
	}, [snapshots]);

	const stalledWorkers = useMemo(() => {
		return activeSnapshots.filter((snapshot) => {
			if (snapshot.status !== 'running' || !snapshot.last_activity_at) return false;
			const ts = Date.parse(snapshot.last_activity_at);
			if (Number.isNaN(ts)) return false;
			return nowMs - ts > 60_000;
		});
	}, [activeSnapshots, nowMs]);

	if (!isOpen) {
		return null;
	}

	return (
		<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 p-4">
			<div className="flex h-[78vh] w-[min(1100px,96vw)] flex-col overflow-hidden rounded-2xl border border-zinc-200 bg-white shadow-2xl dark:border-zinc-800 dark:bg-zinc-950">
				<div className="flex items-center justify-between border-b border-zinc-200 px-5 py-4 dark:border-zinc-800">
					<div>
						<div className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">子智能体运行监控</div>
						<div className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">显示最近子智能体快照、超时剩余时间、最近错误与恢复记录。</div>
					</div>
					<div className="flex items-center gap-2">
						<button
							onClick={() => void loadSnapshots()}
							className="rounded-md border border-zinc-200 p-2 text-zinc-600 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-900"
							title="刷新"
						>
							<RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
						</button>
						<button
							onClick={onClose}
							className="rounded-md border border-zinc-200 p-2 text-zinc-600 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-900"
							title="关闭"
						>
							<X className="h-4 w-4" />
						</button>
					</div>
				</div>

				<div className="grid grid-cols-6 gap-3 border-b border-zinc-200 px-5 py-4 dark:border-zinc-800">
					<div className="rounded-xl bg-zinc-50 p-3 dark:bg-zinc-900">
						<div className="text-[11px] uppercase tracking-wide text-zinc-500">智能体总数</div>
						<div className="mt-1 text-xl font-semibold text-zinc-900 dark:text-zinc-100">{summary?.total ?? 0}</div>
					</div>
					<div className="rounded-xl bg-emerald-50 p-3 dark:bg-emerald-950/40">
						<div className="text-[11px] uppercase tracking-wide text-emerald-700 dark:text-emerald-300">运行中</div>
						<div className="mt-1 text-xl font-semibold text-emerald-800 dark:text-emerald-200">{summary?.running ?? 0}</div>
					</div>
					<div className="rounded-xl bg-amber-50 p-3 dark:bg-amber-950/40">
						<div className="text-[11px] uppercase tracking-wide text-amber-700 dark:text-amber-300">待处理</div>
						<div className="mt-1 text-xl font-semibold text-amber-800 dark:text-amber-200">{summary?.pending ?? 0}</div>
					</div>
					<div className="rounded-xl bg-blue-50 p-3 dark:bg-blue-950/40">
						<div className="text-[11px] uppercase tracking-wide text-blue-700 dark:text-blue-300">已完成</div>
						<div className="mt-1 text-xl font-semibold text-blue-800 dark:text-blue-200">{summary?.completed ?? 0}</div>
					</div>
					<div className="rounded-xl bg-red-50 p-3 dark:bg-red-950/40">
						<div className="text-[11px] uppercase tracking-wide text-red-700 dark:text-red-300">失败</div>
						<div className="mt-1 text-xl font-semibold text-red-800 dark:text-red-200">{summary?.failed ?? 0}</div>
					</div>
					<div className="rounded-xl bg-orange-50 p-3 dark:bg-orange-950/40">
						<div className="text-[11px] uppercase tracking-wide text-orange-700 dark:text-orange-300">停滞 60s+</div>
						<div className="mt-1 text-xl font-semibold text-orange-800 dark:text-orange-200">{stalledWorkers.length}</div>
					</div>
				</div>

				{error && (
					<div className="mx-5 mt-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300">
						{error}
					</div>
				)}

				<div className="flex-1 overflow-y-auto px-5 py-4">
					{activeSnapshots.length === 0 && recentTerminalSnapshots.length === 0 && !loading ? (
						<div className="flex h-full items-center justify-center text-sm text-zinc-500 dark:text-zinc-400">当前没有可展示的子智能体快照</div>
					) : (
						<div className="space-y-4">
							{activeSnapshots.length > 0 && (
								<div>
									<div className="mb-3 text-xs font-semibold uppercase tracking-wide text-zinc-500 dark:text-zinc-400">运行中</div>
									<div className="space-y-4">
										{activeSnapshots.map((snapshot) => {
								const hasProviderIssue = Boolean(snapshot.last_error || snapshot.failure_type || snapshot.retryable_failure);
								const remaining = snapshot.has_deadline ? formatRemaining(snapshot.deadline_unix, nowMs) : '无';
								return (
									<div key={snapshot.worker_id} className="rounded-2xl border border-zinc-200 bg-white p-4 shadow-sm dark:border-zinc-800 dark:bg-zinc-950">
										<div className="flex items-start justify-between gap-4">
											<div className="min-w-0 flex-1">
												<div className="flex items-center gap-2">
													<Bot className="h-4 w-4 text-blue-500" />
													<div className="truncate text-sm font-semibold text-zinc-900 dark:text-zinc-100">{formatWorkerRole(snapshot.role)}</div>
													<span className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${getStatusTone(snapshot.status, snapshot.archived)}`}>
														{formatStatusLabel(snapshot.status, snapshot.archived)}
													</span>
													{hasProviderIssue && (
														<span className="inline-flex items-center gap-1 rounded-full bg-red-50 px-2 py-0.5 text-[11px] font-medium text-red-700 dark:bg-red-950/30 dark:text-red-300">
															<AlertTriangle className="h-3.5 w-3.5" />
															{formatFailureType(snapshot.failure_type)}
														</span>
													)}
												</div>
												<div className="mt-2 text-xs text-zinc-500 dark:text-zinc-400 break-all">智能体 ID：{snapshot.worker_id}</div>
												<div className="mt-1 text-xs text-zinc-500 dark:text-zinc-400 break-all">任务 ID：{snapshot.task_id}</div>
												{snapshot.parent_session_id && (
													<div className="mt-1 text-xs text-zinc-500 dark:text-zinc-400 break-all">父会话：{snapshot.parent_session_id}</div>
												)}
											</div>
											<div className="shrink-0 rounded-xl bg-zinc-50 px-3 py-2 text-right dark:bg-zinc-900">
												<div className="flex items-center justify-end gap-1 text-[11px] text-zinc-500 dark:text-zinc-400">
													<Timer className="h-3.5 w-3.5" />
													剩余
												</div>
												<div className="mt-1 text-sm font-semibold text-zinc-900 dark:text-zinc-100">{remaining}</div>
												<div className="mt-1 text-[11px] text-zinc-500 dark:text-zinc-400">最后活跃 {formatAgo(snapshot.last_activity_at, nowMs)}</div>
											</div>
										</div>

										<div className="mt-4 grid grid-cols-2 gap-3 text-xs text-zinc-600 dark:text-zinc-300">
											<div className="rounded-xl bg-zinc-50 px-3 py-2 dark:bg-zinc-900">
												<div className="text-zinc-500 dark:text-zinc-400">超时窗口</div>
												<div className="mt-1 font-medium">{snapshot.timeout_seconds} 秒</div>
											</div>
											<div className="rounded-xl bg-zinc-50 px-3 py-2 dark:bg-zinc-900">
												<div className="text-zinc-500 dark:text-zinc-400">重试</div>
												<div className="mt-1 font-medium">{formatRetrySummary(snapshot)}</div>
											</div>
										</div>

										{snapshot.structured_summary && (
											<div className="mt-3 rounded-xl border border-blue-100 bg-blue-50 px-3 py-2 text-xs text-blue-800 dark:border-blue-900 dark:bg-blue-950/30 dark:text-blue-200">
												<div className="font-medium">结构化摘要</div>
												<div className="mt-1 whitespace-pre-wrap break-words">{snapshot.structured_summary}</div>
											</div>
										)}

										{snapshot.last_error && (
											<div className="mt-3 rounded-xl border border-red-100 bg-red-50 px-3 py-2 text-xs text-red-800 dark:border-red-900 dark:bg-red-950/30 dark:text-red-200">
												<div className="font-medium">最近错误</div>
												<div className="mt-1 whitespace-pre-wrap break-words">{snapshot.last_error}</div>
											</div>
										)}

										{snapshot.last_recovery_note && (
											<div className="mt-3 rounded-xl border border-amber-100 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200">
												<div className="font-medium">最近恢复说明</div>
												<div className="mt-1 whitespace-pre-wrap break-words">{snapshot.last_recovery_note}</div>
											</div>
										)}
									</div>
								);
										})}
									</div>
								</div>
							)}

							{recentTerminalSnapshots.length > 0 && (
								<div>
									<div className="mb-3 text-xs font-semibold uppercase tracking-wide text-zinc-500 dark:text-zinc-400">当前任务已结束</div>
									<div className="space-y-4">
										{recentTerminalSnapshots.map((snapshot) => {
											const endedAt = snapshot.completed_unix ? new Date(snapshot.completed_unix * 1000).toLocaleString('zh-CN') : '未知';
											const hasFailure = isFailureSnapshot(snapshot);
											return (
												<div key={snapshot.worker_id} className="rounded-2xl border border-zinc-200 bg-white p-4 shadow-sm dark:border-zinc-800 dark:bg-zinc-950">
													<div className="flex items-start justify-between gap-4">
														<div className="min-w-0 flex-1">
															<div className="flex items-center gap-2">
																<Bot className="h-4 w-4 text-blue-500" />
																<div className="truncate text-sm font-semibold text-zinc-900 dark:text-zinc-100">{formatWorkerRole(snapshot.role)}</div>
																<span className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${getStatusTone(snapshot.status, snapshot.archived)}`}>
																	{formatStatusLabel(snapshot.status, snapshot.archived)}
																</span>
															</div>
															<div className="mt-2 text-xs text-zinc-500 dark:text-zinc-400 break-all">智能体 ID：{snapshot.worker_id}</div>
															<div className="mt-1 text-xs text-zinc-500 dark:text-zinc-400 break-all">任务 ID：{snapshot.task_id}</div>
															<div className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">结束时间：{endedAt}</div>
														</div>
														<div className={`shrink-0 rounded-xl px-3 py-2 text-right ${hasFailure ? 'bg-red-50 dark:bg-red-950/30' : 'bg-blue-50 dark:bg-blue-950/30'}`}>
															<div className="text-[11px] text-zinc-500 dark:text-zinc-400">结果</div>
															<div className={`mt-1 text-sm font-semibold ${hasFailure ? 'text-red-800 dark:text-red-200' : 'text-blue-800 dark:text-blue-200'}`}>
																{hasFailure ? '失败' : '完成'}
															</div>
														</div>
													</div>

													{snapshot.structured_summary && (
														<div className={`mt-3 rounded-xl border px-3 py-2 text-xs ${hasFailure ? 'border-red-100 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950/30 dark:text-red-200' : 'border-blue-100 bg-blue-50 text-blue-800 dark:border-blue-900 dark:bg-blue-950/30 dark:text-blue-200'}`}>
															<div className="font-medium">结构化摘要</div>
															<div className="mt-1 whitespace-pre-wrap break-words">{snapshot.structured_summary}</div>
														</div>
													)}

													{snapshot.last_error && (
														<div className="mt-3 rounded-xl border border-red-100 bg-red-50 px-3 py-2 text-xs text-red-800 dark:border-red-900 dark:bg-red-950/30 dark:text-red-200">
															<div className="font-medium">最近错误</div>
															<div className="mt-1 whitespace-pre-wrap break-words">{snapshot.last_error}</div>
														</div>
													)}
												</div>
											);
										})}
									</div>
								</div>
							)}
						</div>
					)}
				</div>
			</div>
		</div>
	);
}
