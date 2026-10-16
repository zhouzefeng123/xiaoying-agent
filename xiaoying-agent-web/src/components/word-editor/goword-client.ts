import type {
	GoWordApplyResult,
	GoWordActionExecutionResult,
	GoWordActionPlanResult,
	GoWordHeaderFooterInput,
	GoWordDocument,
	GoWordIRDocument,
	GoWordDocumentLayout,
	GoWordParagraphStyle,
	GoWordLayoutOptions,
	GoWordSaveResult,
	GoWordSearchResult,
	GoWordSectionLayoutPatch,
	GoWordSession,
	GoWordTableCellInput,
	GoWordTableColumnInput,
	GoWordTableInput,
	GoWordTableRowInput,
	GoWordTextStyle,
	WordEditorActionPayload,
} from './types';

const API_BASE_URL = '';

export type GoWordOperation = {
	type: 'replace_text' | 'insert_paragraph' | 'insert_paragraph_break' | 'insert_line_break' | 'insert_table' | 'insert_table_row' | 'delete_table_row' | 'insert_table_column' | 'delete_table_column' | 'update_table_cell' | 'update_header_footer' | 'update_text_style' | 'update_paragraph_style' | 'update_section_layout' | 'delete_paragraph' | 'scroll_to';
	paragraph_id?: string;
	anchor_id?: string;
	table_id?: string;
	position?: 'before' | 'after';
	row_index?: number;
	cell_index?: number;
	start_offset?: number;
	end_offset?: number;
	before_text?: string;
	text?: string;
	style_patch?: GoWordTextStyle;
	paragraph_style_patch?: GoWordParagraphStyle;
	paragraph?: {
		text: string;
		style?: GoWordParagraphStyle;
		runs?: Array<{
			id: string;
			text: string;
			style?: GoWordTextStyle;
		}>;
	};
	table?: GoWordTableInput;
	table_row?: GoWordTableRowInput;
	table_column?: GoWordTableColumnInput;
	table_cell?: GoWordTableCellInput;
	header_footer?: GoWordHeaderFooterInput;
	section_layout_patch?: GoWordSectionLayoutPatch;
	track_changes?: boolean;
};

type UploadedFileResponse = {
	path: string;
	url: string;
};

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
	const response = await fetch(`${API_BASE_URL}${path}`, {
		headers: {
			'Content-Type': 'application/json',
			...(init?.headers ?? {}),
		},
		...init,
	});

	if (!response.ok) {
		const message = (await response.text()).trim();
		throw new Error(message || `请求失败: ${response.status}`);
	}

	return response.json() as Promise<T>;
}

export function getAbsoluteFilePath(file?: File | null): string | undefined {
	if (!file) {
		return undefined;
	}

	const fileWithPath = file as File & { _absolutePath?: string; path?: string };
	const candidate = fileWithPath._absolutePath ?? fileWithPath.path;
	if (!candidate || !candidate.trim()) {
		return undefined;
	}

	return candidate;
}

export async function uploadWordDocument(file: File): Promise<UploadedFileResponse> {
	const formData = new FormData();
	formData.append('file', file);

	const response = await fetch(`${API_BASE_URL}/v1/upload`, {
		method: 'POST',
		body: formData,
	});

	if (!response.ok) {
		const message = (await response.text()).trim();
		throw new Error(message || '上传 Word 文档失败');
	}

	return response.json() as Promise<UploadedFileResponse>;
}

export async function openGoWordDocument(path: string): Promise<GoWordSession> {
	return requestJSON<GoWordSession>('/v1/goword/open', {
		method: 'POST',
		body: JSON.stringify({ path }),
	});
}

export async function getGoWordDocument(sessionId: string): Promise<GoWordSession> {
	return requestJSON<GoWordSession>(`/v1/goword/document?session_id=${encodeURIComponent(sessionId)}`);
}

export async function getGoWordIR(sessionId: string): Promise<GoWordIRDocument> {
	return requestJSON<GoWordIRDocument>(`/v1/goword/ir?session_id=${encodeURIComponent(sessionId)}`);
}

export async function applyGoWordOperations(sessionId: string, operations: GoWordOperation[], expectedRevision: number): Promise<GoWordApplyResult> {
	return requestJSON<GoWordApplyResult>('/v1/goword/apply', {
		method: 'POST',
		body: JSON.stringify({ session_id: sessionId, expected_revision: expectedRevision, operations }),
	});
}

export async function closeGoWordSession(sessionId: string): Promise<void> {
	await requestJSON<{ status: string }>('/v1/goword/close', {
		method: 'POST',
		body: JSON.stringify({ session_id: sessionId }),
	});
}

export async function saveGoWordSession(sessionId: string, path?: string): Promise<GoWordSaveResult> {
	return requestJSON<GoWordSaveResult>('/v1/goword/save', {
		method: 'POST',
		body: JSON.stringify({ session_id: sessionId, path: path ?? '' }),
	});
}

export async function searchGoWordDocument(
	sessionId: string,
	request: {
		query: string;
		scope?: 'line' | 'paragraph' | 'table' | 'all';
		mode?: 'contains' | 'exact';
		case_sensitive?: boolean;
		limit?: number;
	},
): Promise<GoWordSearchResult> {
	return requestJSON<GoWordSearchResult>('/v1/goword/search', {
		method: 'POST',
		body: JSON.stringify({ session_id: sessionId, ...request }),
	});
}

export async function getGoWordLayout(sessionId: string, options?: GoWordLayoutOptions): Promise<GoWordDocumentLayout> {
	return requestJSON<GoWordDocumentLayout>('/v1/goword/layout', {
		method: 'POST',
		body: JSON.stringify({
			session_id: sessionId,
			options: options ?? {},
		}),
	});
}

export async function planGoWordAction(sessionId: string, action: WordEditorActionPayload): Promise<GoWordActionPlanResult> {
	return requestJSON<GoWordActionPlanResult>('/v1/goword/plan', {
		method: 'POST',
		body: JSON.stringify({ session_id: sessionId, action }),
	});
}

export async function executeGoWordActionPlan(sessionId: string, action: WordEditorActionPayload): Promise<GoWordActionExecutionResult> {
	return requestJSON<GoWordActionExecutionResult>('/v1/goword/execute_plan', {
		method: 'POST',
		body: JSON.stringify({ session_id: sessionId, action }),
	});
}

export async function validateGoWordActionResult(request: {
	action: WordEditorActionPayload;
	result: GoWordApplyResult;
	activeParagraphId?: string | null;
}): Promise<GoWordActionExecutionResult['validation']> {
	return requestJSON<GoWordActionExecutionResult['validation']>('/v1/goword/validate', {
		method: 'POST',
		body: JSON.stringify({
			action: request.action,
			result: request.result,
			active_paragraph_id: request.activeParagraphId ?? '',
		}),
	});
}

export function buildGoWordImageURL(sessionId: string, relationshipId?: string, target?: string): string {
	const params = new URLSearchParams({ session_id: sessionId });
	if (relationshipId && relationshipId.trim()) {
		params.set('relationship_id', relationshipId.trim());
	}
	if (target && target.trim()) {
		params.set('target', target.trim());
	}
	return `${API_BASE_URL}/v1/goword/image?${params.toString()}`;
}